package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
)

// —— 协作写作：写作台在线状态（谁在编辑哪一章、是否有未保存的修改）与实时事件（章节被保存、目录变化），
// 以及保存时的冲突保护：写作台保存时回传打开章节时的内容摘要（base_hash），期间有他人保存过则返回 409 与最新内容，
// 由写作台自动合并或让作者选择。事件按书推送，多实例时经 cluster 送达其他实例上的写作台。——

const (
	presenceHeartbeat = 20 * time.Second
	presenceTTL       = 70 * time.Second // 超过此时间没有心跳的在线记录视为已离开（实例异常退出时）
	collabConnHeader  = "X-Collab-Conn"  // 保存请求携带的写作台连接 ID，事件据此让发起者忽略自己的操作
)

var writerHub = eventhub.New("writer.books", 64)

var errDocConflict = errors.New("document changed")

// DocumentHash 章节标题与正文的摘要（写作台据此判断打开后是否有他人保存过）。
func documentHash(title, content string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + content))
	return hex.EncodeToString(sum[:16])
}

// parentKey 父章节的比较键（顶级章节为空串）。
func parentKey(id *uint) string {
	if id == nil {
		return ""
	}
	return strconv.FormatUint(uint64(*id), 10)
}

func withContentHash(doc *models.Document) *models.Document {
	doc.ContentHash = documentHash(doc.Title, doc.Content)
	return doc
}

type collabUser struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

func toCollabUser(u *models.User) collabUser {
	return collabUser{ID: u.ID, Username: u.Username, DisplayName: u.PublicName(), Avatar: u.Avatar}
}

// docConflict 保存冲突：返回最新的章节内容与最后保存人（取最近一条历史版本）。
func (a *App) respondDocConflict(c *gin.Context, docID uint) {
	var latest models.Document
	a.DB.First(&latest, docID)
	withContentHash(&latest)
	data := gin.H{"document": latest}
	var rev models.DocumentRevision
	if a.DB.Where("document_id = ?", docID).Order("id DESC").First(&rev).Error == nil {
		var u models.User
		if a.DB.First(&u, rev.UserID).Error == nil {
			data["saved_by"] = toCollabUser(&u)
		}
		data["saved_at"] = rev.CreatedAt
	}
	c.JSON(http.StatusConflict, gin.H{"success": false, "code": "DOC_CONFLICT", "message": "章节已被他人修改", "data": data})
}

// —— 在线状态 ——

type presenceView struct {
	ConnID string     `json:"conn_id"`
	User   collabUser `json:"user"`
	DocID  uint       `json:"doc_id"`
	Dirty  bool       `json:"dirty"`
	Since  time.Time  `json:"since"`
}

// bookPresence 一本书当前在线的写作台（清理过期记录）。
func (a *App) bookPresence(bookID uint) []presenceView {
	cutoff := time.Now().Add(-presenceTTL)
	a.DB.Where("seen_at < ?", cutoff).Delete(&models.WriterPresence{})
	var rows []models.WriterPresence
	a.DB.Where("book_id = ?", bookID).Order("created_at ASC").Find(&rows)
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	users := map[uint]*models.User{}
	if len(ids) > 0 {
		var list []models.User
		a.DB.Where("id IN ?", ids).Find(&list)
		for i := range list {
			users[list[i].ID] = &list[i]
		}
	}
	out := make([]presenceView, 0, len(rows))
	for _, r := range rows {
		if u := users[r.UserID]; u != nil {
			out = append(out, presenceView{ConnID: r.ConnID, User: toCollabUser(u), DocID: r.DocID, Dirty: r.Dirty, Since: r.CreatedAt})
		}
	}
	return out
}

func (a *App) publishPresence(bookID uint) {
	writerHub.Publish(bookID, "presence", gin.H{"presence": a.bookPresence(bookID)})
}

// publishDocSaved 章节被保存（标题或正文有变化）：通知同一本书的其他写作台。
func (a *App) publishDocSaved(c *gin.Context, bookID uint, doc *models.Document) {
	u := currentUser(c)
	if u == nil {
		return
	}
	writerHub.Publish(bookID, "doc.saved", gin.H{
		"doc_id": doc.ID, "title": doc.Title, "content_hash": documentHash(doc.Title, doc.Content),
		"by": toCollabUser(u), "at": time.Now(), "origin": c.GetHeader(collabConnHeader),
	})
}

// publishTreeChanged 目录有变化（新建、删除、移动、改名）：通知其他写作台刷新目录。
func (a *App) publishTreeChanged(c *gin.Context, bookID uint) {
	writerHub.Publish(bookID, "tree", gin.H{"origin": c.GetHeader(collabConnHeader)})
}

// WriterCollabStream GET /books/:id/collab/stream?ticket=&doc_id= 写作台事件流：建立即登记在线，推送在线列表与事件；断开即离开。
func (a *App) WriterCollabStream(c *gin.Context) {
	u := a.streamUser(c)
	if u == nil || !authz.Has(u.Role, authz.DocumentUpdate) {
		failStream(c)
		return
	}
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "无权编辑该书籍")
		return
	}
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	connID := hex.EncodeToString(buf)
	docID, _ := strconv.ParseUint(c.Query("doc_id"), 10, 64)
	now := time.Now()
	if err := a.DB.Create(&models.WriterPresence{ConnID: connID, BookID: book.ID, UserID: u.ID, DocID: uint(docID), SeenAt: now}).Error; err != nil {
		fail(c, http.StatusInternalServerError, "登记在线状态失败")
		return
	}
	ch := writerHub.Subscribe(book.ID)
	defer func() {
		writerHub.Unsubscribe(book.ID, ch)
		a.DB.Delete(&models.WriterPresence{}, "conn_id = ?", connID)
		a.publishPresence(book.ID)
	}()

	eventhub.StartSSE(c)
	write := func(name string, v any) {
		raw, _ := json.Marshal(v)
		eventhub.Write(c.Writer, name, raw)
	}
	write("hello", gin.H{"conn_id": connID, "presence": a.bookPresence(book.ID)})
	a.publishPresence(book.ID)

	heartbeat := time.NewTicker(presenceHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return // 消费过慢被断开：浏览器重连后重新登记
			}
			eventhub.Write(c.Writer, ev.Name, ev.Data)
		case <-heartbeat.C:
			a.DB.Model(&models.WriterPresence{}).Where("conn_id = ?", connID).Update("seen_at", time.Now())
			eventhub.Ping(c.Writer)
		}
	}
}

// UpdateWriterPresence POST /books/:id/collab/presence {conn_id, doc_id, dirty} 切换章节或修改状态变化时更新在线状态。
func (a *App) UpdateWriterPresence(c *gin.Context) {
	var req struct {
		ConnID string `json:"conn_id"`
		DocID  uint   `json:"doc_id"`
		Dirty  bool   `json:"dirty"`
	}
	if c.ShouldBindJSON(&req) != nil || req.ConnID == "" {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	res := a.DB.Model(&models.WriterPresence{}).Where("conn_id = ? AND user_id = ? AND book_id = ?", req.ConnID, u.ID, book.ID).
		Updates(map[string]any{"doc_id": req.DocID, "dirty": req.Dirty, "seen_at": time.Now()})
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, "写作台连接已断开")
		return
	}
	a.publishPresence(book.ID)
	ok(c, gin.H{"conn_id": req.ConnID})
}
