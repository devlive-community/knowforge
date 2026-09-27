package webhooks

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(pluginKey)
	with := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermUse), h}
	}
	api.GET("/webhooks", with(b.List)...)
	api.POST("/webhooks", with(b.Create)...)
	api.PUT("/webhooks/:id", with(b.Update)...)
	api.DELETE("/webhooks/:id", with(b.Delete)...)
	api.POST("/webhooks/:id/test", with(b.Test)...)
	api.POST("/webhooks/:id/secret", with(b.RotateSecret)...)
	api.GET("/webhooks/:id/deliveries", with(b.Deliveries)...)
	api.POST("/webhooks/deliveries/:id/redeliver", with(b.Redeliver)...)
}

func newSecret() string {
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	return "whsec_" + hex.EncodeToString(raw)
}

// myHook 当前用户的订阅。
func (b *behavior) myHook(c *gin.Context) (Hook, bool) {
	var h Hook
	if b.core.Gorm().Where("id = ? AND user_id = ?", c.Param("id"), b.core.CurrentUser(c).ID).First(&h).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "Webhook 不存在")
		return h, false
	}
	return h, true
}

// hookRequest 创建/修改请求（修改时字段可选）。
type hookRequest struct {
	URL    *string   `json:"url"`
	Events *[]string `json:"events"`
	BookID *uint     `json:"book_id"`
	Active *bool     `json:"active"`
}

// apply 校验并写入订阅字段。
func (b *behavior) apply(c *gin.Context, u *models.User, h *Hook, req hookRequest) bool {
	fail := func(msg string) bool { b.core.Fail(c, http.StatusBadRequest, msg); return false }
	if req.URL != nil {
		clean, err := validateURL(*req.URL)
		if err != nil {
			return fail(err.Error())
		}
		h.URL = clean
	}
	if req.Events != nil {
		seen := map[string]bool{}
		events := []string{}
		for _, e := range *req.Events {
			known := false
			for _, k := range Events {
				known = known || k == e
			}
			if !known {
				return fail("不支持的事件：" + e)
			}
			if !seen[e] {
				seen[e] = true
				events = append(events, e)
			}
		}
		if len(events) == 0 {
			return fail("请至少选择一个事件")
		}
		h.Events = events
	}
	if req.BookID != nil {
		if *req.BookID != 0 {
			var book models.Book
			if b.core.Gorm().First(&book, *req.BookID).Error != nil || book.UserID != u.ID {
				return fail("只能订阅自己的书籍")
			}
		}
		h.BookID = *req.BookID
	}
	if req.Active != nil {
		h.Active = *req.Active
		if h.Active {
			h.Failures, h.DisabledReason = 0, "" // 重新启用时清空失败计数
		}
	}
	return true
}

// List GET /webhooks 我的订阅、可订阅的事件与数量上限。
func (b *behavior) List(c *gin.Context) {
	u := b.core.CurrentUser(c)
	var hooks []Hook
	b.core.Gorm().Where("user_id = ?", u.ID).Order("id DESC").Find(&hooks)
	b.core.OK(c, gin.H{"items": hooks, "events": Events, "limit": plugincore.EntitlementValue(b.core, u, entMax)})
}

// Create POST /webhooks {url, events[], book_id?} 创建订阅，返回 {item, secret（只返回这一次）}。
func (b *behavior) Create(c *gin.Context) {
	var req hookRequest
	if c.ShouldBindJSON(&req) != nil || req.URL == nil || req.Events == nil {
		b.core.Fail(c, http.StatusBadRequest, "请填写地址并选择事件")
		return
	}
	u := b.core.CurrentUser(c)
	if limit := plugincore.EntitlementValue(b.core, u, entMax); limit != plugincore.Unlimited {
		var n int64
		b.core.Gorm().Model(&Hook{}).Where("user_id = ?", u.ID).Count(&n)
		if n >= limit {
			b.core.Fail(c, http.StatusForbidden, "Webhook 数量已达上限，可删除不用的订阅，或升级等级、开通会员获得更多")
			return
		}
	}
	h := Hook{UserID: u.ID, Active: true, Secret: newSecret()}
	if !b.apply(c, u, &h, req) {
		return
	}
	if err := b.core.Gorm().Create(&h).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	b.core.OK(c, gin.H{"item": h, "secret": h.Secret})
}

// Update PUT /webhooks/:id {url?, events?, book_id?, active?}
func (b *behavior) Update(c *gin.Context) {
	h, found := b.myHook(c)
	if !found {
		return
	}
	var req hookRequest
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if !b.apply(c, b.core.CurrentUser(c), &h, req) {
		return
	}
	b.core.Gorm().Save(&h)
	b.core.OK(c, h)
}

// Delete DELETE /webhooks/:id 删除订阅与其投递记录。
func (b *behavior) Delete(c *gin.Context) {
	h, found := b.myHook(c)
	if !found {
		return
	}
	db := b.core.Gorm()
	db.Where("hook_id = ?", h.ID).Delete(&Delivery{})
	db.Delete(&h)
	b.core.OK(c, gin.H{"deleted": true})
}

// Test POST /webhooks/:id/test 投递一条 ping 事件（订阅停用时也可测试）。
func (b *behavior) Test(c *gin.Context) {
	h, found := b.myHook(c)
	if !found {
		return
	}
	d, err := b.enqueue(h, EventPing, map[string]any{"message": "这是一条测试投递", "hook_id": h.ID})
	if err != nil {
		b.core.Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	b.core.OK(c, d)
}

// RotateSecret POST /webhooks/:id/secret 重置签名密钥，返回新密钥（只返回这一次）。
func (b *behavior) RotateSecret(c *gin.Context) {
	h, found := b.myHook(c)
	if !found {
		return
	}
	h.Secret = newSecret()
	b.core.Gorm().Model(&Hook{}).Where("id = ?", h.ID).Update("secret", h.Secret)
	b.core.OK(c, gin.H{"secret": h.Secret})
}

// Deliveries GET /webhooks/:id/deliveries?page=&page_size= 投递记录（新→旧，保留 30 天）。
func (b *behavior) Deliveries(c *gin.Context) {
	h, found := b.myHook(c)
	if !found {
		return
	}
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Delivery{}).Where("hook_id = ?", h.ID)
	var total int64
	q.Count(&total)
	var rows []Delivery
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	b.core.OK(c, gin.H{"items": rows, "total": total, "page": page, "page_size": pageSize})
}

// Redeliver POST /webhooks/deliveries/:id/redeliver 以相同内容重新投递（新建一次投递）。
func (b *behavior) Redeliver(c *gin.Context) {
	db := b.core.Gorm()
	var old Delivery
	var h Hook
	if db.First(&old, c.Param("id")).Error != nil || db.First(&h, old.HookID).Error != nil || h.UserID != b.core.CurrentUser(c).ID {
		b.core.Fail(c, http.StatusNotFound, "投递记录不存在")
		return
	}
	d := Delivery{HookID: h.ID, Event: old.Event, Payload: old.Payload, Status: "pending", CreatedAt: time.Now()}
	if err := db.Create(&d).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	if q := b.core.JobQueue(); q != nil {
		_, _ = q.EnqueueOwned(c.Request.Context(), h.UserID, jobDeliver, deliverPayload{DeliveryID: d.ID}, maxAttempts+1)
	}
	b.core.OK(c, d)
}
