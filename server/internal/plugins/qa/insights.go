package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/ai"
	instances "knowforge/server/internal/cluster"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 作者洞察：汇总读者就本书向 AI 与社区提出的问题——相近的问题归为一类（问题向量相似度；没有向量时按关键词），
// 找出书中答不上来的问题（内容缺口）、问题最多的章节与尚未回答的社区提问，帮助作者改进内容。
// 问题向量由巡检与作者打开洞察页时在后台补算，尚未计算的按关键词归类。
// 读者的 AI 提问只以匿名文字出现（不含提问者），作者与协作者自己的提问不计入；管理员可关闭 AI 提问的汇总。
// 能否查看为权益（qa.insights）；每周有新提问时给作者发一次站内摘要。

const (
	cfgInsightsAsks   = "qa_insights_asks"   // 是否把读者的 AI 提问（匿名）汇总给作者，默认开
	cfgInsightsDigest = "qa_insights_digest" // 每周摘要通知，默认开
	entInsights       = "qa.insights"

	clusterCosine  = 0.82 // 问题向量相似度达到即归为一类
	clusterJaccard = 0.5  // 没有向量时的关键词重合度
	maxClusters    = 20
	maxSamples     = 5
	questionBatch  = 32
)

// InsightDigest 每周摘要的发送记录（每本书每周一次）。
type InsightDigest struct {
	ID     uint   `gorm:"primaryKey"`
	BookID uint   `gorm:"uniqueIndex:idx_qa_digest"`
	Week   string `gorm:"size:10;uniqueIndex:idx_qa_digest"` // 2026-W39
}

func (InsightDigest) TableName() string { return "qa_insight_digests" }

func (b *behavior) insightsShareAsks() bool { return b.core.GetSetting(cfgInsightsAsks) != "false" }

// —— 问题向量（后台计算，供归类）——

func insightText(title, body string) string {
	return truncate(strings.TrimSpace(title+"\n"+body), embedMaxRunes)
}

// embedQuestions 为本书尚未向量化的提问（AI 提问与社区提问）计算向量；未配置嵌入模型时跳过。系统调用。
func (b *behavior) embedQuestions(ctx context.Context, bookID uint) {
	if _, embed := b.core.AIStatus(); !embed {
		return
	}
	ctx = ai.WithCaller(ctx, ai.Caller{Feature: "qa.insights", RefType: "book", RefID: bookID})
	db := b.core.Gorm()
	for {
		var asks []Ask
		db.Select("id, question").Where("book_id = ? AND status = ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", bookID, askDone).Limit(questionBatch).Find(&asks)
		var qs []Question
		if len(asks) < questionBatch {
			db.Select("id, title, body").Where("book_id = ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", bookID).Limit(questionBatch - len(asks)).Find(&qs)
		}
		if len(asks)+len(qs) == 0 {
			return
		}
		texts := make([]string, 0, len(asks)+len(qs))
		for _, a := range asks {
			texts = append(texts, insightText(a.Question, ""))
		}
		for _, q := range qs {
			texts = append(texts, insightText(q.Title, q.Body))
		}
		vecs, _, err := b.core.AIEmbed(ctx, texts)
		if err != nil || len(vecs) != len(texts) {
			return // 下次巡检重试；归类时暂按关键词
		}
		for i, a := range asks {
			db.Model(&Ask{}).Where("id = ?", a.ID).Update("q_embedding", encodeVector(vecs[i]))
		}
		for i, q := range qs {
			db.Model(&Question{}).Where("id = ?", q.ID).Update("q_embedding", encodeVector(vecs[len(asks)+i]))
		}
	}
}

var embeddingBooks sync.Map // 正在补算问题向量的书籍（同一本书不并发补算）

// embedQuestionsLater 作者打开洞察页时在后台为本书补算问题向量（提问流程本身不增加模型调用；其余由巡检补齐）。
func (b *behavior) embedQuestionsLater(bookID uint) {
	if _, busy := embeddingBooks.LoadOrStore(bookID, true); busy {
		return
	}
	lease := fmt.Sprintf("qa.embed-questions:%d", bookID)
	if !instances.TryLease(lease, 30*time.Minute) { // 其他实例正在补算
		embeddingBooks.Delete(bookID)
		return
	}
	go func() {
		defer func() {
			_ = recover()
			instances.ReleaseLease(lease)
			embeddingBooks.Delete(bookID)
		}()
		b.embedQuestions(context.Background(), bookID)
	}()
}

// —— 归类 ——

type insightItem struct {
	kind      string // ask | question
	id        uint
	text      string
	docIDs    []uint
	gap       bool // AI 提问：书中没有找到相关内容（没有出处）
	open      bool // 社区提问：尚未解决
	vec       []float32
	terms     map[string]bool
	createdAt time.Time
}

func termSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range terms(s) {
		out[t] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

func similar(a, b *insightItem) bool {
	if len(a.vec) > 0 && len(b.vec) > 0 {
		return cosine(a.vec, b.vec) >= clusterCosine
	}
	return jaccard(a.terms, b.terms) >= clusterJaccard
}

type cluster struct {
	items []*insightItem
}

// clusterItems 按时间从新到旧逐个归入第一个足够相似的类（与该类代表问题比较），否则新建一类。
func clusterItems(items []*insightItem) []*cluster {
	var out []*cluster
	for _, it := range items {
		placed := false
		for _, c := range out {
			if similar(c.items[0], it) {
				c.items = append(c.items, it)
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, &cluster{items: []*insightItem{it}})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].items) > len(out[j].items) })
	return out
}

type chapterRef struct {
	ID    uint   `json:"id"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

type topicView struct {
	Question  string       `json:"question"` // 代表问题
	Count     int          `json:"count"`
	Asks      int          `json:"asks"`
	Community int          `json:"community"`
	Gaps      int          `json:"gaps"`
	Samples   []string     `json:"samples"`
	Chapters  []chapterRef `json:"chapters"`
	LastAt    time.Time    `json:"last_at"`
}

// Insights GET /qa/books/:id/insights?days=7|30|90 作者洞察。
func (b *behavior) Insights(c *gin.Context) {
	book, found := b.readableBook(c)
	if !found {
		return
	}
	u := b.core.CurrentUser(c)
	if !b.core.CanEditBookContent(u, book) {
		b.core.Fail(c, http.StatusForbidden, "只有作者与协作者可以查看")
		return
	}
	if plugincore.EntitlementValue(b.core, u, entInsights) <= 0 {
		b.core.Fail(c, http.StatusForbidden, "当前等级/会员不含读者问题洞察，提升等级或开通会员后可用")
		return
	}
	days := b.core.AtoiDefault(c.Query("days"), 30)
	if days != 7 && days != 30 && days != 90 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days)
	db := b.core.Gorm()

	// 作者与协作者自己的提问不计入
	editors := map[uint]bool{}
	isEditor := func(id uint) bool {
		v, ok := editors[id]
		if !ok {
			var user models.User
			v = db.First(&user, id).Error == nil && b.core.CanEditBookContent(&user, book)
			editors[id] = v
		}
		return v
	}

	var items []*insightItem
	stats := gin.H{}
	if b.insightsShareAsks() {
		var asks []Ask
		db.Select("id, user_id, doc_id, question, citations, calls, q_embedding, created_at").
			Where("book_id = ? AND status = ? AND created_at >= ?", book.ID, askDone, since).Order("id DESC").Find(&asks)
		askers := map[uint]bool{}
		gaps := 0
		for _, a := range asks {
			if isEditor(a.UserID) {
				continue
			}
			var cites []Citation
			_ = json.Unmarshal([]byte(a.Citations), &cites)
			it := &insightItem{kind: "ask", id: a.ID, text: a.Question, gap: len(cites) == 0, vec: decodeVector(a.QEmbedding), terms: termSet(a.Question), createdAt: a.CreatedAt}
			if a.DocID != 0 {
				it.docIDs = append(it.docIDs, a.DocID)
			}
			for _, ct := range cites {
				it.docIDs = append(it.docIDs, ct.DocID)
			}
			if it.gap {
				gaps++
			}
			askers[a.UserID] = true
			items = append(items, it)
		}
		stats["ai_asks"], stats["askers"], stats["gaps"] = len(items), len(askers), gaps
	}
	var questions []Question
	db.Where("book_id = ? AND visibility = ? AND created_at >= ?", book.ID, "", since).Order("id DESC").Find(&questions)
	community, open := 0, 0
	for _, q := range questions {
		if isEditor(q.UserID) {
			continue
		}
		it := &insightItem{kind: "question", id: q.ID, text: q.Title, open: q.Status != "resolved", vec: decodeVector(q.QEmbedding), terms: termSet(q.Title + " " + q.Body), createdAt: q.CreatedAt}
		if q.DocID != 0 {
			it.docIDs = []uint{q.DocID}
		}
		community++
		if it.open {
			open++
		}
		items = append(items, it)
	}
	stats["community"], stats["open"] = community, open
	sort.SliceStable(items, func(i, j int) bool { return items[i].createdAt.After(items[j].createdAt) })

	docs := map[uint]models.Document{}
	docOf := func(id uint) (models.Document, bool) {
		d, ok := docs[id]
		if !ok {
			if db.Select("id, title, slug, status").Where("id = ? AND book_id = ?", id, book.ID).First(&d).Error != nil {
				return d, false
			}
			docs[id] = d
		}
		return d, true
	}
	chapterRefs := func(counts map[uint]int, limit int) []chapterRef {
		out := []chapterRef{}
		for id, n := range counts {
			if d, ok := docOf(id); ok {
				out = append(out, chapterRef{ID: d.ID, Title: d.Title, Slug: d.Slug, Count: n})
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].ID < out[j].ID
		})
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}

	topics := []topicView{}
	gapTopics := []topicView{}
	allChapters := map[uint]int{}
	for _, cl := range clusterItems(items) {
		t := topicView{Question: cl.items[0].text, Samples: []string{}, LastAt: cl.items[0].createdAt}
		counts := map[uint]int{}
		seenText := map[string]bool{}
		for _, it := range cl.items {
			t.Count++
			if it.kind == "ask" {
				t.Asks++
			} else {
				t.Community++
			}
			if it.gap {
				t.Gaps++
			}
			perItem := map[uint]bool{}
			for _, id := range it.docIDs {
				if !perItem[id] {
					perItem[id] = true
					counts[id]++
					allChapters[id]++
				}
			}
			if key := strings.TrimSpace(it.text); len(t.Samples) < maxSamples && !seenText[key] {
				seenText[key] = true
				t.Samples = append(t.Samples, key)
			}
		}
		t.Chapters = chapterRefs(counts, 3)
		if len(topics) < maxClusters {
			topics = append(topics, t)
		}
		if t.Gaps*2 >= t.Asks && t.Gaps > 0 {
			gapTopics = append(gapTopics, t)
		}
	}
	sort.SliceStable(gapTopics, func(i, j int) bool { return gapTopics[i].Gaps > gapTopics[j].Gaps })
	if len(gapTopics) > maxClusters {
		gapTopics = gapTopics[:maxClusters]
	}

	// 尚未回答的社区提问（最早的在前）
	var unanswered []Question
	db.Where("book_id = ? AND visibility = ? AND status <> ? AND answer_count = 0", book.ID, "", "resolved").Order("id ASC").Limit(20).Find(&unanswered)
	openList := make([]gin.H, 0, len(unanswered))
	for _, q := range unanswered {
		openList = append(openList, gin.H{"id": q.ID, "title": q.Title, "created_at": q.CreatedAt})
	}
	var pending, pendingQ int64
	db.Model(&Ask{}).Where("book_id = ? AND status = ? AND created_at >= ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", book.ID, askDone, since).Count(&pending)
	db.Model(&Question{}).Where("book_id = ? AND created_at >= ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", book.ID, since).Count(&pendingQ)
	if pending+pendingQ > 0 {
		b.embedQuestionsLater(book.ID)
	}
	_, embed := b.core.AIStatus()
	b.core.OK(c, gin.H{
		"days": days, "stats": stats, "include_asks": b.insightsShareAsks(), "semantic": embed,
		"topics": topics, "gaps": gapTopics, "chapters": chapterRefs(allChapters, 10), "unanswered": openList,
	})
}

// —— 每周摘要 ——

func isoWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// sendWeeklyDigests 每本书每周一次：过去 7 天有读者提问时通知作者（AI 提问数、新的社区提问与尚未回答数）。
func (b *behavior) sendWeeklyDigests() {
	if b.core.GetSetting(cfgInsightsDigest) == "false" {
		return
	}
	db := b.core.Gorm()
	now := time.Now()
	week := isoWeek(now)
	since := now.AddDate(0, 0, -7)
	var bookIDs []uint
	db.Model(&Ask{}).Where("created_at >= ? AND status = ?", since, askDone).Distinct().Pluck("book_id", &bookIDs)
	var qBooks []uint
	db.Model(&Question{}).Where("created_at >= ? AND visibility = ?", since, "").Distinct().Pluck("book_id", &qBooks)
	seen := map[uint]bool{}
	for _, id := range append(bookIDs, qBooks...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		var book models.Book
		if db.First(&book, id).Error != nil {
			continue
		}
		var owner models.User
		if db.First(&owner, book.UserID).Error != nil || plugincore.EntitlementValue(b.core, &owner, entInsights) <= 0 {
			continue
		}
		var asks, questions, open int64
		if b.insightsShareAsks() {
			db.Model(&Ask{}).Where("book_id = ? AND status = ? AND created_at >= ? AND user_id <> ?", id, askDone, since, book.UserID).Count(&asks)
		}
		db.Model(&Question{}).Where("book_id = ? AND visibility = ? AND created_at >= ? AND user_id <> ?", id, "", since, book.UserID).Count(&questions)
		db.Model(&Question{}).Where("book_id = ? AND visibility = ? AND status <> ? AND answer_count = 0", id, "", "resolved").Count(&open)
		if asks+questions == 0 {
			continue
		}
		if res := db.Create(&InsightDigest{BookID: id, Week: week}); res.Error != nil {
			continue // 本周已发送
		}
		b.core.NotifyI18n(book.UserID, "system", "notify.qa.weeklyInsights",
			map[string]string{"book": book.Title, "asks": fmt.Sprint(asks), "questions": fmt.Sprint(questions), "open": fmt.Sprint(open)},
			map[string]any{"link": "/book/settings/" + book.Slug + "/reader-questions"})
	}
}

// sweepInsights 巡检：补算问题向量、发送每周摘要。
func sweepInsights(core plugincore.Core) {
	b := &behavior{core: core}
	if !core.PluginEnabled(plugins.KeyQA) {
		return
	}
	db := core.Gorm()
	var bookIDs []uint
	db.Model(&Ask{}).Where("status = ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", askDone).Distinct().Limit(200).Pluck("book_id", &bookIDs)
	for _, id := range bookIDs {
		b.embedQuestions(context.Background(), id)
	}
	b.sendWeeklyDigests()
}
