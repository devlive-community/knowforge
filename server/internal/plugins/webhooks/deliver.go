package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

const (
	requestTimeout = 10 * time.Second // 单次投递的 HTTP 超时
	responseKeep   = 1024             // 保存的响应体长度
	userAgent      = "KnowForge-Webhook/1"
)

var errPrivateAddress = errors.New("不允许投递到内网或本机地址")

// allowPrivateNetwork 是否允许投递到内网（内网部署时由运维经环境变量开启）。
func allowPrivateNetwork() bool { return os.Getenv("KNOWFORGE_WEBHOOK_ALLOW_PRIVATE") == "true" }

// isPrivateIP 内网、本机、链路本地、组播、未指定与运营商级 NAT 地址。
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}

// httpClient 投递用的客户端：连接时解析并校验目标地址（防止经 DNS 指向内网），不跟随重定向，不使用系统代理。
func httpClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	allow := allowPrivateNetwork()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("无法解析 %s", host)
			}
			if !allow {
				for _, ip := range ips {
					if isPrivateIP(ip.IP) {
						return nil, errPrivateAddress
					}
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: requestTimeout,
	}
	return &http.Client{Transport: transport, Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// validateURL 订阅地址：http/https，地址不是内网字面量（域名在投递时再校验解析结果）。
func validateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 500 {
		return "", errors.New("请填写以 http:// 或 https:// 开头的有效地址（不超过 500 字符）")
	}
	if !allowPrivateNetwork() {
		host := u.Hostname()
		if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
			return "", errPrivateAddress
		}
		if ip := net.ParseIP(host); ip != nil && isPrivateIP(ip) {
			return "", errPrivateAddress
		}
	}
	return raw, nil
}

// sign 签名：HMAC-SHA256(密钥, "时间戳.请求体")，十六进制。
func sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// —— 事件 ——

func (b *behavior) siteURL() string { return strings.TrimRight(b.core.GetSetting("site_url"), "/") }

func (b *behavior) bookInfo(book *models.Book) map[string]any {
	return map[string]any{"id": book.ID, "slug": book.Slug, "title": book.Title, "url": b.siteURL() + "/book/detail/" + book.Slug}
}

func (b *behavior) chapterInfo(book *models.Book, doc *models.Document) map[string]any {
	return map[string]any{"id": doc.ID, "slug": doc.Slug, "title": doc.Title, "url": b.siteURL() + "/book/reader/" + book.Slug + "/" + doc.Slug}
}

func userInfo(u *models.User) map[string]any {
	if u == nil {
		return nil
	}
	return map[string]any{"id": u.ID, "username": u.Username, "name": u.PublicName()}
}

func (b *behavior) onComment(ev plugincore.ActivityEvent) {
	id, _ := strconv.ParseUint(ev.SourceID, 10, 64)
	db := b.core.Gorm()
	var comment models.Comment
	var doc models.Document
	var book models.Book
	if db.Preload("User").First(&comment, id).Error != nil || db.First(&doc, comment.DocumentID).Error != nil || db.First(&book, doc.BookID).Error != nil {
		return
	}
	content := []rune(comment.Content)
	if len(content) > 500 {
		content = append(content[:500], '…')
	}
	b.emit(ev.UserID, book.ID, EventCommentReceived, map[string]any{
		"book": b.bookInfo(&book), "chapter": b.chapterInfo(&book, &doc),
		"comment": map[string]any{"id": comment.ID, "content": string(content), "author": userInfo(comment.User), "created_at": comment.CreatedAt},
	})
}

func (b *behavior) onReaction(ev plugincore.ActivityEvent) {
	id, _ := strconv.ParseUint(ev.SourceID, 10, 64)
	db := b.core.Gorm()
	var r models.Reaction
	var book models.Book
	if db.Preload("User").First(&r, id).Error != nil || db.First(&book, r.BookID).Error != nil {
		return
	}
	b.emit(ev.UserID, book.ID, EventReactionReceived, map[string]any{
		"book": b.bookInfo(&book), "reaction": map[string]any{"id": r.ID, "type": r.Type, "user": userInfo(r.User), "created_at": r.CreatedAt},
	})
}

// dataUint 读取活动详情中的 ID（发出方写入的是 uint）。
func dataUint(data map[string]any, key string) uint {
	switch v := data[key].(type) {
	case uint:
		return v
	case int:
		return uint(v)
	case float64:
		return uint(v)
	}
	return 0
}

// bookAndChapter 活动详情中的书籍与章节（章节可无）。
func (b *behavior) bookAndChapter(data map[string]any) (*models.Book, map[string]any, bool) {
	var book models.Book
	if b.core.Gorm().First(&book, dataUint(data, "book_id")).Error != nil {
		return nil, nil, false
	}
	var chapter map[string]any
	var doc models.Document
	if id := dataUint(data, "doc_id"); id > 0 && b.core.Gorm().First(&doc, id).Error == nil {
		chapter = b.chapterInfo(&book, &doc)
	}
	return &book, chapter, true
}

// onSale 作品被购买：金额为订单金额与作者到手金额（最小货币单位）。
func (b *behavior) onSale(ev plugincore.ActivityEvent) {
	book, chapter, ok := b.bookAndChapter(ev.Data)
	if !ok {
		return
	}
	b.emit(ev.UserID, book.ID, EventSaleCompleted, map[string]any{
		"book": b.bookInfo(book), "chapter": chapter,
		"sale": map[string]any{"order_no": ev.Data["order_no"], "title": ev.Data["title"], "amount_cents": ev.Data["amount_cents"],
			"net_cents": ev.Data["net_cents"], "currency": ev.Data["currency"], "sold_at": ev.Data["sold_at"]},
	})
}

// onQuestion 书籍收到公开提问。
func (b *behavior) onQuestion(ev plugincore.ActivityEvent) {
	book, chapter, ok := b.bookAndChapter(ev.Data)
	if !ok {
		return
	}
	question, _ := ev.Data["question"].(map[string]any)
	if question == nil {
		return
	}
	out := make(map[string]any, len(question))
	for k, v := range question {
		out[k] = v
	}
	if link, ok := out["link"].(string); ok && strings.HasPrefix(link, "/") {
		out["url"] = b.siteURL() + link // 与书籍、章节一致给出完整地址
	}
	delete(out, "link")
	b.emit(ev.UserID, book.ID, EventQuestionReceived, map[string]any{"book": b.bookInfo(book), "chapter": chapter, "question": out})
}

// emit 为用户订阅了该事件（且范围包含该书）的启用中订阅创建投递。
func (b *behavior) emit(userID, bookID uint, event string, data map[string]any) {
	if userID == 0 || !b.core.PluginEnabled(pluginKey) {
		return
	}
	var hooks []Hook
	b.core.Gorm().Where("user_id = ? AND active = ? AND (book_id = 0 OR book_id = ?)", userID, true, bookID).Find(&hooks)
	for _, h := range hooks {
		for _, e := range h.Events {
			if e == event {
				_, _ = b.enqueue(h, event, data)
				break
			}
		}
	}
}

// enqueue 创建一次投递并交给任务队列。
func (b *behavior) enqueue(h Hook, event string, data map[string]any) (Delivery, error) {
	db := b.core.Gorm()
	d := Delivery{HookID: h.ID, Event: event, Status: "pending", CreatedAt: time.Now()}
	if err := db.Create(&d).Error; err != nil {
		return d, err
	}
	body, _ := json.Marshal(map[string]any{"id": d.ID, "event": event, "created_at": d.CreatedAt.UTC().Format(time.RFC3339), "data": data})
	d.Payload = string(body)
	db.Model(&Delivery{}).Where("id = ?", d.ID).Update("payload", d.Payload)
	q := b.core.JobQueue()
	if q == nil {
		return d, errors.New("任务队列未就绪")
	}
	_, err := q.EnqueueOwned(context.Background(), h.UserID, jobDeliver, deliverPayload{DeliveryID: d.ID}, maxAttempts+1)
	return d, err
}

type deliverPayload struct {
	DeliveryID uint `json:"delivery_id"`
}

// deliver 任务：投递一次；失败时返回错误由任务队列退避重试，用尽次数后记为失败（连续失败过多则停用订阅并通知）。
func (b *behavior) deliver(ctx context.Context, raw json.RawMessage) error {
	var p deliverPayload
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	db := b.core.Gorm()
	var d Delivery
	var h Hook
	if db.First(&d, p.DeliveryID).Error != nil || d.Status == "success" || db.First(&h, d.HookID).Error != nil {
		return nil
	}
	status, respBody, duration, err := post(ctx, h, d)
	now := time.Now()
	d.Attempts++
	updates := map[string]any{"attempts": d.Attempts, "response_status": status, "response_body": respBody, "duration_ms": duration, "delivered_at": now}
	if err == nil && status >= 200 && status < 300 {
		updates["status"], updates["error"] = "success", ""
		db.Model(&Delivery{}).Where("id = ?", d.ID).Updates(updates)
		db.Model(&Hook{}).Where("id = ?", h.ID).Updates(map[string]any{"failures": 0, "last_status": "success", "last_delivery_at": now})
		return nil
	}
	msg := fmt.Sprintf("响应状态 %d", status)
	if err != nil {
		msg = err.Error()
	}
	updates["error"] = truncate(msg, 500)
	if d.Attempts < maxAttempts && !errors.Is(err, errPrivateAddress) {
		db.Model(&Delivery{}).Where("id = ?", d.ID).Updates(updates)
		return errors.New(msg) // 由任务队列退避后重试
	}
	updates["status"] = "failed"
	db.Model(&Delivery{}).Where("id = ?", d.ID).Updates(updates)
	failures := h.Failures + 1
	hookUpdates := map[string]any{"failures": failures, "last_status": "failed", "last_delivery_at": now}
	if failures >= disableAfter && h.Active {
		hookUpdates["active"], hookUpdates["disabled_reason"] = false, fmt.Sprintf("连续 %d 次投递失败", failures)
		b.core.NotifyI18n(h.UserID, "system", "notify.webhooks.disabled", map[string]string{"url": h.URL, "n": strconv.Itoa(failures)}, map[string]any{"link": "/user/webhooks"})
	}
	db.Model(&Hook{}).Where("id = ?", h.ID).Updates(hookUpdates)
	return nil
}

// post 发出一次请求，返回响应码、响应体片段与耗时。
func post(ctx context.Context, h Hook, d Delivery) (int, string, int64, error) {
	body := []byte(d.Payload)
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-KnowForge-Event", d.Event)
	req.Header.Set("X-KnowForge-Delivery", strconv.FormatUint(uint64(d.ID), 10))
	req.Header.Set("X-KnowForge-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-KnowForge-Signature", "sha256="+sign(h.Secret, ts, body))
	started := time.Now()
	resp, err := httpClient().Do(req)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && errors.Is(urlErr.Err, errPrivateAddress) {
			return 0, "", duration, errPrivateAddress
		}
		return 0, "", duration, err
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, responseKeep))
	return resp.StatusCode, string(snippet), duration, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
