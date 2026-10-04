package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 章节批量操作：一次请求提交所选章节，服务端按给定顺序逐个处理，并以事件流（SSE）实时推送每个章节的结果与进度，
// 前端据此逐行切换状态、显示进度条，不再由浏览器逐个发请求。

const maxBatchDocuments = 1000

// batchItemResult 一个章节的处理结果。
type batchItemResult struct {
	ID      uint   `json:"id"`
	OK      bool   `json:"ok"`
	Status  string `json:"status,omitempty"`  // 改状态：处理后的状态（被发布守卫拦截时为原状态）
	Held    string `json:"held,omitempty"`    // 被发布守卫拦截的说明
	Skipped bool   `json:"skipped,omitempty"` // 删除：已随父章节删除
	Error   string `json:"error,omitempty"`
}

// changeDocumentStatus 修改章节状态（可级联整棵子树），与 PUT /documents/:id 只改状态时的行为一致：
// 发布守卫审查（拦截则保持未发布）、级联子章节、草稿书籍随章节发布提升为连载中、首次发布触发发布钩子。
func (a *App) changeDocumentStatus(actor *models.User, book *models.Book, doc *models.Document, status string, cascade bool) (string, error) {
	oldStatus := doc.Status
	doc.Status = status
	publishedChapter := status == "published"
	cascadeStatus := ""
	if cascade {
		cascadeStatus = status
	}
	held := ""
	if doc.Status == "published" && oldStatus != "published" {
		if v := plugincore.CheckPublish(a, documentPublishTarget(book, doc, actor.ID)); v.Hold {
			held = v.Message
			doc.Status = oldStatus
			publishedChapter = false
			if cascadeStatus == "published" {
				cascadeStatus = ""
			}
		}
	}
	var cascaded []uint
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Document{}).Where("id = ?", doc.ID).Update("status", doc.Status).Error; err != nil {
			return err
		}
		if cascadeStatus != "" {
			descendants := subtreeDocIDs(tx, doc.ID)
			if cascadeStatus == "published" {
				tx.Model(&models.Document{}).Where("id IN ? AND status <> ?", descendants, "published").Pluck("id", &cascaded)
			}
			if len(descendants) > 0 {
				if err := tx.Model(&models.Document{}).Where("id IN ?", descendants).Update("status", cascadeStatus).Error; err != nil {
					return err
				}
			}
		}
		if publishedChapter && book.Status == "draft" {
			if err := tx.Model(&models.Book{}).Where("id = ?", book.ID).Update("status", "in_progress").Error; err != nil {
				return err
			}
			book.Status = "in_progress"
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	a.emitActivity(doc.UserID, "document.updated", "document", strconv.FormatUint(uint64(doc.ID), 10), fmt.Sprintf("document.updated:%d:%d", doc.ID, time.Now().UnixNano()))
	for _, id := range cascaded {
		var child models.Document
		if a.DB.First(&child, id).Error == nil {
			a.GuardDocumentPublish(book, &child, actor.ID)
		}
	}
	if publishedChapter && oldStatus != "published" {
		plugincore.FireChapterPublished(a, book, doc)
	}
	return held, nil
}

// trashDocumentSubtree 把章节及其整棵子树移入回收站（同一批次，便于整体恢复），返回移入的章节数与保留截止时间。
func (a *App) trashDocumentSubtree(actor *models.User, doc *models.Document) (int, time.Time, error) {
	ids := append([]uint{doc.ID}, subtreeDocIDs(a.DB, doc.ID)...)
	now := currentTime()
	err := a.DB.Model(&models.Document{}).Where("id IN ?", ids).Updates(map[string]any{
		"deleted_at": now, "deleted_by": actor.ID, "trash_group": randomSlug("trash"),
	}).Error
	return len(ids), now.Add(trashRetention), err
}

// batchRequest 批量操作的公共校验：书籍可编辑、章节数量有效；通过后返回书籍与当前用户。
func (a *App) batchRequest(c *gin.Context, ids []uint) (*models.Book, *models.User, bool) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return nil, nil, false
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "无权操作该书籍")
		return nil, nil, false
	}
	if len(ids) == 0 || len(ids) > maxBatchDocuments {
		fail(c, http.StatusBadRequest, fmt.Sprintf("请选择 1 到 %d 个章节", maxBatchDocuments))
		return nil, nil, false
	}
	return book, u, true
}

// streamBatch 逐个处理章节并推送事件：start {total}，每个章节 item {result, done, total}，结束时 done {total, done, failed}。
func streamBatch(c *gin.Context, ids []uint, handle func(id uint) batchItemResult) {
	eventhub.StartSSE(c)
	write := func(name string, v any) {
		raw, _ := json.Marshal(v)
		eventhub.Write(c.Writer, name, raw)
	}
	write("start", gin.H{"total": len(ids)})
	failed := 0
	for i, id := range ids {
		if c.Request.Context().Err() != nil {
			return // 客户端断开：已处理的保留，其余不再处理
		}
		r := handle(id)
		if !r.OK {
			failed++
		}
		write("item", gin.H{"result": r, "done": i + 1, "total": len(ids)})
	}
	write("done", gin.H{"total": len(ids), "done": len(ids) - failed, "failed": failed})
}

// BatchDocumentStatus POST /books/:id/documents/batch-status {ids[], status} 批量改状态（含子章节），以事件流推送进度。
func (a *App) BatchDocumentStatus(c *gin.Context) {
	var req struct {
		IDs    []uint `json:"ids"`
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&req) != nil || !docStatuses[req.Status] {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	book, u, ok := a.batchRequest(c, req.IDs)
	if !ok {
		return
	}
	defer a.publishTreeChanged(c, book.ID) // 处理完后通知其他写作台刷新目录
	streamBatch(c, req.IDs, func(id uint) batchItemResult {
		var doc models.Document
		if a.DB.Where("id = ? AND book_id = ?", id, book.ID).First(&doc).Error != nil {
			return batchItemResult{ID: id, Error: "章节不存在"}
		}
		held, err := a.changeDocumentStatus(u, book, &doc, req.Status, true)
		if err != nil {
			return batchItemResult{ID: id, Error: "保存失败"}
		}
		return batchItemResult{ID: id, OK: true, Status: doc.Status, Held: held}
	})
}

// BatchDeleteDocuments POST /books/:id/documents/batch-delete {ids[]} 批量移入回收站（含子章节），以事件流推送进度；
// 与单个删除一样需要二次认证（开启时）。已随父章节删除的章节记为跳过。
func (a *App) BatchDeleteDocuments(c *gin.Context) {
	if !a.requireStepUp(c, tfOpDelete) {
		return
	}
	var req struct {
		IDs []uint `json:"ids"`
	}
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	book, u, ok := a.batchRequest(c, req.IDs)
	if !ok {
		return
	}
	defer a.publishTreeChanged(c, book.ID) // 处理完后通知其他写作台刷新目录
	streamBatch(c, req.IDs, func(id uint) batchItemResult {
		var doc models.Document
		if a.DB.Unscoped().Where("id = ? AND book_id = ?", id, book.ID).First(&doc).Error != nil {
			return batchItemResult{ID: id, Error: "章节不存在"}
		}
		if doc.DeletedAt.Valid {
			return batchItemResult{ID: id, OK: true, Skipped: true}
		}
		count, _, err := a.trashDocumentSubtree(u, &doc)
		if err != nil {
			return batchItemResult{ID: id, Error: "删除失败"}
		}
		a.recordActivity(book.ID, doc.ID, u.ID, "doc.deleted", map[string]any{"title": doc.Title, "count": count})
		return batchItemResult{ID: id, OK: true}
	})
}
