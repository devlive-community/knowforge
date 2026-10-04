package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/cluster"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
)

// M13 站内通知：
//   - 触发源：评论、点赞/收藏、系统升级完成（协作邀请待 M14 接入）
//   - SSE 实时推送：内存 hub（单实例架构，多实例时需外置广播）
//   - EventSource 无法携带 Authorization 头，stream 端点支持 ?token= 鉴权

// notificationHub 每个用户一组 SSE 订阅 channel
type notificationHub struct {
	sync.Mutex
	subs map[uint]map[chan string]struct{}
}

func newNotificationHub() *notificationHub {
	return &notificationHub{subs: map[uint]map[chan string]struct{}{}}
}

func (h *notificationHub) subscribe(userID uint) chan string {
	ch := make(chan string, 64) // 任务进度等高频消息也经此推送
	h.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = map[chan string]struct{}{}
	}
	h.subs[userID][ch] = struct{}{}
	h.Unlock()
	return ch
}

func (h *notificationHub) unsubscribe(userID uint, ch chan string) {
	h.Lock()
	if set, ok := h.subs[userID]; ok {
		delete(set, ch)
		if len(set) == 0 {
			delete(h.subs, userID)
		}
	}
	h.Unlock()
}

// broadcast 推送给本实例与其他实例上该用户的订阅者。
func (h *notificationHub) broadcast(userID uint, message string) {
	h.deliver(userID, message)
	if raw, err := json.Marshal(remoteNotification{UserID: userID, Message: message}); err == nil {
		cluster.Broadcast(channelNotifications, raw)
	}
}

// deliver 推送给本实例上该用户的订阅者。
func (h *notificationHub) deliver(userID uint, message string) {
	h.Lock()
	for ch := range h.subs[userID] {
		select {
		case ch <- message:
		default: // 订阅端积压时丢弃，靠下次全量同步兜底
		}
	}
	h.Unlock()
}

// Notify 创建通知并实时推送给在线订阅者；失败静默（通知不应阻断主流程）
func (a *App) Notify(userID uint, ntype, title string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, _ := json.Marshal(payload)
	n := models.Notification{UserID: userID, Type: ntype, Title: title, Payload: string(raw)}
	if err := a.DB.Create(&n).Error; err != nil {
		return
	}
	a.Notifications.broadcast(userID, fmt.Sprintf(`{"notification":{"id":%d,"type":%q,"title":%q,"payload":%s,"read_at":null,"created_at":%q}}`,
		n.ID, n.Type, n.Title, string(raw), n.CreatedAt.Format(time.RFC3339)))
	// 站内通知同时按用户偏好发邮件（总开关关闭或未绑定邮箱时静默跳过）
	a.maybeSendNotificationEmail(userID, ntype, title, payload)
}

type notificationItem struct {
	ID        uint            `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Payload   json.RawMessage `json:"payload"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListNotifications GET /notifications?page=&per_page=&unread=true 当前用户通知列表
func (a *App) ListNotifications(c *gin.Context) {
	u := currentUser(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "10"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 50 {
		perPage = 10
	}

	query := a.DB.Model(&models.Notification{}).Where("user_id = ?", u.ID)
	if c.Query("unread") == "true" {
		query = query.Where("read_at IS NULL")
	}
	var total int64
	query.Count(&total)

	items := []notificationItem{}
	var rows []models.Notification
	if err := query.Order("created_at DESC").Limit(perPage).Offset((page - 1) * perPage).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "获取通知失败")
		return
	}
	for _, r := range rows {
		items = append(items, notificationItem{
			ID: r.ID, Type: r.Type, Title: r.Title,
			Payload: json.RawMessage(defaultObject(r.Payload)),
			ReadAt:  r.ReadAt, CreatedAt: r.CreatedAt,
		})
	}

	var unread int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", u.ID).Count(&unread)
	ok(c, gin.H{
		"notifications": items,
		"total":         total,
		"unread_count":  unread,
		"page":          page,
		"per_page":      perPage,
	})
}

// defaultObject 空的 payload JSON 补成 {}，保证前端拿到合法 JSON 对象
func defaultObject(raw string) string {
	if raw == "" {
		return "{}"
	}
	return raw
}

type notificationsReadRequest struct {
	IDs []uint `json:"ids"`
	All bool   `json:"all"`
}

// MarkNotificationsRead POST /notifications/read 标记已读（ids 或 all）
func (a *App) MarkNotificationsRead(c *gin.Context) {
	u := currentUser(c)
	var req notificationsReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if !req.All && len(req.IDs) == 0 {
		fail(c, http.StatusBadRequest, "请指定 ids 或 all")
		return
	}
	query := a.DB.Model(&models.Notification{}).
		Where("user_id = ? AND read_at IS NULL", u.ID)
	if !req.All {
		query = query.Where("id IN ?", req.IDs)
	}
	if err := query.Update("read_at", currentTime()).Error; err != nil {
		fail(c, http.StatusInternalServerError, "标记失败")
		return
	}
	var unread int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", u.ID).Count(&unread)
	ok(c, gin.H{"unread_count": unread})
}

// SSENotifications GET /notifications/stream SSE 实时通知流
// EventSource 无法设置请求头，鉴权支持 ?token= 或 Authorization 头
func (a *App) SSENotifications(c *gin.Context) {
	// 鉴权：请求头/Cookie 登录态或 ?ticket= 短时事件流凭证（不接受 URL 中的登录令牌）
	user := a.streamUser(c)
	if user == nil || !authz.Has(user.Role, authz.NotificationRead) {
		failStream(c)
		return
	}
	userID := user.ID

	var unread int64
	a.DB.Model(&models.Notification{}).Where("user_id = ? AND read_at IS NULL", userID).Count(&unread)

	eventhub.StartSSE(c)

	flusher, canFlush := c.Writer.(http.Flusher)
	if !canFlush {
		return
	}
	// 连接建立即推送当前未读数，前端据此同步徽标
	fmt.Fprintf(c.Writer, "data: {\"unread_count\":%d}\n\n", unread)
	flusher.Flush()

	ch := a.Notifications.subscribe(userID)
	defer a.Notifications.unsubscribe(userID, ch)

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case message := <-ch:
			fmt.Fprintf(c.Writer, "data: %s\n\n", message)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// NotifyAdminsOnUpgrade 启动时检测版本变化，向所有管理员发送升级完成通知
func (a *App) NotifyAdminsOnUpgrade() {
	previous := a.getSetting("version")
	if previous == "" || previous == Version {
		return
	}
	var admins []models.User
	if err := a.DB.Where("role = ?", "admin").Find(&admins).Error; err != nil {
		return
	}
	for _, admin := range admins {
		a.NotifyI18n(admin.ID, "system", "notify.system.upgraded", map[string]string{"version": Version},
			map[string]any{"link": "/admin/system", "from_version": previous})
	}
}
