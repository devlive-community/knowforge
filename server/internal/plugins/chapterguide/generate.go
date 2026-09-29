package chapterguide

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/clause"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 导读与概览经任务队列逐个生成（一章一个任务，不设超时），状态变化经书籍维度的 SSE 推给作者的管理页（见 handlers.go）。
// 内容不变时复用已有导读，不重复调用模型；作者编辑过的导读不被自动更新覆盖。

const (
	jobChapter  = "chapterguide.chapter"
	jobOverview = "chapterguide.overview"

	maxSourceRunes   = 30000 // 章节正文交给模型的长度（超出部分省略，导读基于前文）
	overviewDocRunes = 600   // 概览中没有导读的章节取开头的长度
	autoDelay        = 90 * time.Second
	sweepDirtyAfter  = 5 * time.Minute
)

var (
	booksHub = eventhub.New("chapterguide.books", 256)
	timersMu sync.Mutex
	timers   = map[uint]*time.Timer{} // 章节 ID → 自动更新的延迟触发（连续保存只生成一次）
)

var errQuota = errors.New("本月导读生成次数已用完，下月恢复，或提升等级/开通会员获得更多次数")

func docHash(d *models.Document) string {
	sum := sha256.Sum256([]byte(d.Title + "\x00" + d.Content))
	return hex.EncodeToString(sum[:])
}

func headRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}

type guideView struct {
	DocID        uint       `json:"doc_id"`
	Summary      string     `json:"summary"`
	Points       []string   `json:"points"`
	Status       string     `json:"status"`
	Error        string     `json:"error"`
	Edited       bool       `json:"edited"`
	Stale        bool       `json:"stale"` // 章节内容已在导读生成后修改
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	GeneratedAt  *time.Time `json:"generated_at"`
}

func toGuideView(g Guide, doc *models.Document) guideView {
	v := guideView{DocID: g.DocID, Summary: g.Summary, Points: []string{}, Status: g.Status, Error: g.Error, Edited: g.Edited,
		InputTokens: g.InputTokens, OutputTokens: g.OutputTokens, GeneratedAt: g.GeneratedAt}
	_ = json.Unmarshal([]byte(g.Points), &v.Points)
	if doc != nil && g.Status == stateReady && g.SourceHash != "" && g.SourceHash != docHash(doc) {
		v.Stale = true
	}
	return v
}

type overviewView struct {
	Content      string     `json:"content"`
	Status       string     `json:"status"`
	Error        string     `json:"error"`
	Edited       bool       `json:"edited"`
	Stale        bool       `json:"stale"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	GeneratedAt  *time.Time `json:"generated_at"`
}

func (b *behavior) publishGuide(bookID uint, g Guide) {
	var doc models.Document
	if b.core.Gorm().First(&doc, g.DocID).Error == nil {
		booksHub.Publish(bookID, "guide", toGuideView(g, &doc))
	}
	b.publishUserTask(bookID)
}

func (b *behavior) publishOverview(bookID uint) {
	var o Overview
	if b.core.Gorm().First(&o, bookID).Error == nil {
		booksHub.Publish(bookID, "overview", b.toOverviewView(o))
	}
	b.publishUserTask(bookID)
}

func (b *behavior) toOverviewView(o Overview) overviewView {
	v := overviewView{Content: o.Content, Status: o.Status, Error: o.Error, Edited: o.Edited, InputTokens: o.InputTokens, OutputTokens: o.OutputTokens, GeneratedAt: o.GeneratedAt}
	if o.Status == stateReady && o.SourceHash != "" {
		if hash, _ := b.overviewSource(o.BookID); hash != o.SourceHash {
			v.Stale = true
		}
	}
	return v
}

// monthUsed 用户本月的生成次数（实际调用了模型的才计）。
func (b *behavior) monthUsed(userID uint) int64 {
	now := time.Now()
	var n int64
	b.core.Gorm().Model(&Run{}).Where("user_id = ? AND created_at >= ?", userID, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())).Count(&n)
	return n
}

// callerFor 按「费用承担方」设置决定用量记在谁名下：author 记在触发生成的作者名下（计入其每月 AI 用量），site 记为系统调用。
func (b *behavior) callerFor(actorID uint, kind string, refID uint) ai.Caller {
	c := ai.Caller{Feature: "chapterguide.overview", RefType: "book", RefID: refID, TraceID: ai.NewTraceID()}
	if kind == "chapter" {
		c.Feature, c.RefType = "chapterguide.chapter", "document"
	}
	if b.costBearer() == bearerAuthor {
		c.UserID = actorID
	}
	return c
}

// —— 排队 ——

type chapterPayload struct {
	DocID   uint `json:"doc_id"`
	ActorID uint `json:"actor_id"`
	Force   bool `json:"force"`
}

type overviewPayload struct {
	BookID  uint `json:"book_id"`
	ActorID uint `json:"actor_id"`
	Force   bool `json:"force"`
}

// enqueueChapter 把章节导读排入任务队列（标记为已排队并推送）。
func (b *behavior) enqueueChapter(bookID, docID, actorID uint, force bool) error {
	q := b.core.JobQueue()
	if q == nil {
		return errors.New("任务队列未就绪")
	}
	db := b.core.Gorm()
	g := Guide{BookID: bookID, DocID: docID}
	db.Where("doc_id = ?", docID).FirstOrCreate(&g)
	db.Model(&g).Updates(map[string]any{"status": stateQueued, "error": "", "dirty_at": nil})
	g.Status, g.Error = stateQueued, ""
	b.publishGuide(bookID, g)
	_, err := q.EnqueueOwned(context.Background(), actorID, jobChapter, chapterPayload{DocID: docID, ActorID: actorID, Force: force}, 1)
	return err
}

func (b *behavior) enqueueOverview(bookID, actorID uint, force bool) error {
	q := b.core.JobQueue()
	if q == nil {
		return errors.New("任务队列未就绪")
	}
	db := b.core.Gorm()
	o := Overview{BookID: bookID}
	db.FirstOrCreate(&o, Overview{BookID: bookID})
	db.Model(&o).Updates(map[string]any{"status": stateQueued, "error": ""})
	b.publishOverview(bookID)
	_, err := q.EnqueueOwned(context.Background(), actorID, jobOverview, overviewPayload{BookID: bookID, ActorID: actorID, Force: force}, 1)
	return err
}

// scheduleAuto 自动模式下章节内容修改后延迟排队（连续保存只触发一次）；标记 dirty_at，服务重启丢失的延迟由巡检补上。
func (b *behavior) scheduleAuto(book *models.Book, doc *models.Document) {
	now := time.Now()
	db := b.core.Gorm()
	db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "doc_id"}}, DoUpdates: clause.Assignments(map[string]any{"dirty_at": now})}).
		Create(&Guide{BookID: book.ID, DocID: doc.ID, DirtyAt: &now})
	bookID, docID, actor := book.ID, doc.ID, book.UserID
	timersMu.Lock()
	defer timersMu.Unlock()
	if t, ok := timers[docID]; ok {
		t.Stop()
	}
	timers[docID] = time.AfterFunc(autoDelay, func() {
		timersMu.Lock()
		delete(timers, docID)
		timersMu.Unlock()
		if b.core.PluginEnabled(pluginKey) {
			_ = b.enqueueChapter(bookID, docID, actor, false)
		}
	})
}

// sweepDirty 巡检：自动模式下修改后超过一段时间仍未排队的导读（如服务重启丢失了延迟触发）。
func (b *behavior) sweepDirty() {
	db := b.core.Gorm()
	if !db.Migrator().HasTable(&Guide{}) {
		return
	}
	var rows []Guide
	db.Where("dirty_at IS NOT NULL AND dirty_at < ?", time.Now().Add(-sweepDirtyAfter)).Find(&rows)
	for _, g := range rows {
		var book models.Book
		if db.First(&book, g.BookID).Error == nil && b.autoEnabled(book.ID) {
			_ = b.enqueueChapter(book.ID, g.DocID, book.UserID, false)
		} else {
			db.Model(&g).Update("dirty_at", nil)
		}
	}
}

func (b *behavior) autoEnabled(bookID uint) bool {
	var s BookSetting
	return b.core.Gorm().First(&s, bookID).Error == nil && s.AutoGenerate
}

// —— 生成 ——

const chapterSystem = "你是书籍编辑，为读者写章节导读。只输出 JSON：{\"summary\": \"…\", \"points\": [\"…\"]}。" +
	"summary 是阅读前的导读（一到两段，约 80 到 200 字）：本章讲什么、为什么值得读、读之前需要了解什么；" +
	"points 是本章要点（3 到 6 条，每条一句话）。使用与章节正文相同的语言，只依据正文，不要编造正文中没有的内容。"

const overviewSystem = "你是书籍编辑，为读者写全书概览，只输出 Markdown：先用一段话介绍这本书讲什么、适合谁读；" +
	"再用「## 主要内容」按章节顺序列出各部分要点（相近的章节可以合并，每条一句话）；最后可用一句话给出阅读建议。" +
	"总长约 300 到 600 字，使用与书籍内容相同的语言，只依据给出的内容，不要编造。"

// parseGuide 解析模型输出的 JSON；不是 JSON 时整段作为导读。
func parseGuide(out string) (string, []string) {
	out = strings.TrimSpace(out)
	if start, end := strings.Index(out, "{"), strings.LastIndex(out, "}"); start >= 0 && end > start {
		var v struct {
			Summary string   `json:"summary"`
			Points  []string `json:"points"`
		}
		if json.Unmarshal([]byte(out[start:end+1]), &v) == nil && strings.TrimSpace(v.Summary) != "" {
			points := []string{}
			for _, p := range v.Points {
				if p = strings.TrimSpace(p); p != "" {
					points = append(points, p)
				}
			}
			return strings.TrimSpace(v.Summary), points
		}
	}
	return out, []string{}
}

func (b *behavior) runChapter(ctx context.Context, raw json.RawMessage) error {
	var p chapterPayload
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	db := b.core.Gorm()
	var doc models.Document
	var book models.Book
	if db.First(&doc, p.DocID).Error != nil || db.First(&book, doc.BookID).Error != nil {
		db.Where("doc_id = ?", p.DocID).Delete(&Guide{})
		return nil
	}
	g := Guide{BookID: book.ID, DocID: doc.ID}
	db.Where("doc_id = ?", doc.ID).FirstOrCreate(&g)
	save := func(updates map[string]any) {
		db.Model(&g).Updates(updates)
		db.First(&g, g.ID)
		b.publishGuide(book.ID, g)
	}
	hash := docHash(&doc)
	switch {
	case strings.TrimSpace(doc.Content) == "":
		save(map[string]any{"status": stateSkipped, "error": "", "dirty_at": nil})
		return nil
	case !p.Force && g.Summary != "" && (g.SourceHash == hash || g.Edited):
		// 内容未变化（或作者编辑过的导读）：复用，不调用模型
		save(map[string]any{"status": stateReady, "error": "", "dirty_at": nil})
		return nil
	}
	var actor models.User
	if db.First(&actor, p.ActorID).Error != nil {
		save(map[string]any{"status": stateFailed, "error": "发起人已不存在", "dirty_at": nil})
		return nil
	}
	if !plugincore.WithinLimit(plugincore.EntitlementValue(b.core, &actor, entMonthly), b.monthUsed(actor.ID)) {
		save(map[string]any{"status": stateFailed, "error": errQuota.Error(), "dirty_at": nil})
		return nil
	}
	save(map[string]any{"status": stateGenerating, "error": "", "dirty_at": nil})
	body, cut := headRunes(doc.Content, maxSourceRunes)
	if cut {
		body += "\n\n（后文省略）"
	}
	prompt := "书名：" + book.Title + "\n章节：" + doc.Title + "\n\n<正文>\n" + body + "\n</正文>"
	caller := b.callerFor(actor.ID, "chapter", doc.ID)
	resp, err := b.core.AIChat(ai.WithCaller(ctx, caller), ai.ChatRequest{System: chapterSystem, Messages: []ai.Message{{Role: "user", Content: prompt}}, Temperature: 0.3})
	if err != nil {
		msg := "AI 服务暂时不可用，请稍后重试"
		if errors.Is(err, ai.ErrQuotaExceeded) {
			msg = err.Error()
		}
		save(map[string]any{"status": stateFailed, "error": msg})
		return nil
	}
	summary, points := parseGuide(resp.Content)
	rawPoints, _ := json.Marshal(points)
	now := time.Now()
	db.Create(&Run{UserID: actor.ID, BookID: book.ID, DocID: doc.ID, Kind: "chapter"})
	save(map[string]any{"status": stateReady, "error": "", "summary": summary, "points": string(rawPoints), "source_hash": hash, "edited": false,
		"model": resp.Model, "input_tokens": resp.Usage.InputTokens, "output_tokens": resp.Usage.OutputTokens, "generated_at": &now})
	return nil
}

// overviewSource 全书概览的素材：已发布章节（目录顺序）的导读或开头，及其内容摘要。
func (b *behavior) overviewSource(bookID uint) (string, string) {
	db := b.core.Gorm()
	var docs []models.Document
	db.Where("book_id = ? AND status = ?", bookID, "published").Order("sort_order ASC, id ASC").Find(&docs)
	docs = treeOrder(docs)
	guides := map[uint]Guide{}
	var rows []Guide
	db.Where("book_id = ? AND status = ?", bookID, stateReady).Find(&rows)
	for _, g := range rows {
		guides[g.DocID] = g
	}
	h := sha256.New()
	var b2 strings.Builder
	for _, d := range docs {
		h.Write([]byte(docHash(&d)))
		b2.WriteString("\n### " + d.Title + "\n")
		if g, ok := guides[d.ID]; ok && g.Summary != "" {
			b2.WriteString(g.Summary + "\n")
		} else if text, cut := headRunes(strings.TrimSpace(d.Content), overviewDocRunes); text != "" {
			b2.WriteString(text)
			if cut {
				b2.WriteString("…")
			}
			b2.WriteString("\n")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), b2.String()
}

func (b *behavior) runOverview(ctx context.Context, raw json.RawMessage) error {
	var p overviewPayload
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	db := b.core.Gorm()
	var book models.Book
	if db.First(&book, p.BookID).Error != nil {
		return nil
	}
	o := Overview{BookID: book.ID}
	db.FirstOrCreate(&o, Overview{BookID: book.ID})
	save := func(updates map[string]any) {
		db.Model(&Overview{}).Where("book_id = ?", book.ID).Updates(updates)
		b.publishOverview(book.ID)
	}
	hash, material := b.overviewSource(book.ID)
	switch {
	case strings.TrimSpace(material) == "":
		save(map[string]any{"status": stateSkipped, "error": "还没有已发布的章节"})
		return nil
	case !p.Force && o.Content != "" && (o.SourceHash == hash || o.Edited):
		save(map[string]any{"status": stateReady, "error": ""})
		return nil
	}
	var actor models.User
	if db.First(&actor, p.ActorID).Error != nil {
		save(map[string]any{"status": stateFailed, "error": "发起人已不存在"})
		return nil
	}
	if !plugincore.WithinLimit(plugincore.EntitlementValue(b.core, &actor, entMonthly), b.monthUsed(actor.ID)) {
		save(map[string]any{"status": stateFailed, "error": errQuota.Error()})
		return nil
	}
	save(map[string]any{"status": stateGenerating, "error": ""})
	prompt := "书名：" + book.Title + "\n简介：" + book.Description + "\n\n各章节（按目录顺序）：\n" + material
	resp, err := b.core.AIChat(ai.WithCaller(ctx, b.callerFor(actor.ID, "overview", book.ID)),
		ai.ChatRequest{System: overviewSystem, Messages: []ai.Message{{Role: "user", Content: prompt}}, Temperature: 0.3})
	if err != nil {
		msg := "AI 服务暂时不可用，请稍后重试"
		if errors.Is(err, ai.ErrQuotaExceeded) {
			msg = err.Error()
		}
		save(map[string]any{"status": stateFailed, "error": msg})
		return nil
	}
	now := time.Now()
	db.Create(&Run{UserID: actor.ID, BookID: book.ID, Kind: "overview"})
	save(map[string]any{"status": stateReady, "error": "", "content": strings.TrimSpace(resp.Content), "source_hash": hash, "edited": false,
		"model": resp.Model, "input_tokens": resp.Usage.InputTokens, "output_tokens": resp.Usage.OutputTokens, "generated_at": &now})
	return nil
}

type treeDoc struct {
	doc   models.Document
	depth int
}

// treeOrder 按目录先序排列章节（父章节在子章节之前，同级按排序）。
func treeOrder(docs []models.Document) []models.Document {
	out := []models.Document{}
	for _, t := range treeWithDepth(docs) {
		out = append(out, t.doc)
	}
	return out
}

func treeWithDepth(docs []models.Document) []treeDoc {
	ids := map[uint]bool{}
	for _, d := range docs {
		ids[d.ID] = true
	}
	children := map[uint][]models.Document{}
	var roots []models.Document
	for _, d := range docs {
		if d.ParentID != nil && ids[*d.ParentID] {
			children[*d.ParentID] = append(children[*d.ParentID], d)
		} else {
			roots = append(roots, d)
		}
	}
	out := make([]treeDoc, 0, len(docs))
	var walk func([]models.Document, int)
	walk = func(list []models.Document, depth int) {
		for _, d := range list {
			out = append(out, treeDoc{doc: d, depth: depth})
			walk(children[d.ID], depth+1)
		}
	}
	walk(roots, 0)
	return out
}
