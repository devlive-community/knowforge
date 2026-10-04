package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
)

// —— 协作动态：记录写作台中的协作操作，供写作台「协作」侧栏按时间查看。同一人在 10 分钟内连续保存同一章合并为一条，
// 累加新增 / 删除的行数（按行比较的近似值）。记录写入后推送 activity 事件，打开的写作台据此刷新。——

const (
	activitySaveMergeWindow = 10 * time.Minute
	activityRetention       = 180 * 24 * time.Hour
)

// lineChanges 按行统计新增与删除的行数（不考虑顺序，按行出现次数比较；用于动态展示的近似值）。
func lineChanges(before, after string) (added, removed int) {
	counts := map[string]int{}
	for _, line := range strings.Split(before, "\n") {
		counts[line]++
	}
	for _, line := range strings.Split(after, "\n") {
		if counts[line] > 0 {
			counts[line]--
		} else {
			added++
		}
	}
	for _, n := range counts {
		removed += n
	}
	return added, removed
}

// recordActivity 记录一条协作动态（失败不影响主操作）。
func (a *App) recordActivity(bookID, docID, userID uint, kind string, detail map[string]any) {
	if bookID == 0 || userID == 0 {
		return
	}
	if kind == "doc.saved" {
		var last models.WriterActivity
		err := a.DB.Where("book_id = ? AND document_id = ? AND user_id = ? AND kind = ? AND updated_at > ?",
			bookID, docID, userID, kind, time.Now().Add(-activitySaveMergeWindow)).Order("updated_at DESC").First(&last).Error
		if err == nil {
			prev := map[string]any{}
			_ = json.Unmarshal([]byte(last.Detail), &prev)
			for _, key := range []string{"added", "removed"} {
				detail[key] = toInt(prev[key]) + toInt(detail[key])
			}
			raw, _ := json.Marshal(detail)
			a.DB.Model(&last).Updates(map[string]any{"detail": string(raw), "updated_at": time.Now()})
			writerHub.Publish(bookID, "activity", gin.H{})
			return
		}
	}
	raw, _ := json.Marshal(detail)
	a.DB.Create(&models.WriterActivity{BookID: bookID, DocumentID: docID, UserID: userID, Kind: kind, Detail: string(raw)})
	writerHub.Publish(bookID, "activity", gin.H{})
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

type writerActivityView struct {
	ID         uint           `json:"id"`
	Kind       string         `json:"kind"`
	DocumentID uint           `json:"document_id"`
	User       collabUser     `json:"user"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// ListWriterActivity GET /books/:id/writer-activity?page=&page_size=&doc_id= 协作动态（最近更新的在前）。
func (a *App) ListWriterActivity(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canSuggestBookContent(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权查看")
		return
	}
	page, size := paginate(c)
	q := a.DB.Model(&models.WriterActivity{}).Where("book_id = ?", book.ID)
	if id, err := strconv.ParseUint(c.Query("doc_id"), 10, 64); err == nil && id > 0 {
		q = q.Where("document_id = ?", id)
	}
	var total int64
	q.Count(&total)
	var rows []models.WriterActivity
	q.Order("updated_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows)
	ids := []uint{}
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	users := map[uint]collabUser{}
	if len(ids) > 0 {
		var list []models.User
		a.DB.Where("id IN ?", ids).Find(&list)
		for i := range list {
			users[list[i].ID] = toCollabUser(&list[i])
		}
	}
	items := make([]writerActivityView, 0, len(rows))
	for _, r := range rows {
		detail := map[string]any{}
		_ = json.Unmarshal([]byte(r.Detail), &detail)
		u, found := users[r.UserID]
		if !found {
			u = collabUser{ID: r.UserID, Username: "deleted", DisplayName: "-"}
		}
		items = append(items, writerActivityView{ID: r.ID, Kind: r.Kind, DocumentID: r.DocumentID, User: u, Detail: detail, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

// purgeOldWriterActivity 清理超过保留期的协作动态（维护任务调用）。
func (a *App) purgeOldWriterActivity(ctx context.Context, now time.Time) error {
	return a.DB.WithContext(ctx).Where("updated_at < ?", now.Add(-activityRetention)).Delete(&models.WriterActivity{}).Error
}
