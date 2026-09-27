package qa

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 全站语义检索（管理员开启，默认关闭）：复用问答的分块向量，为核心的站内搜索与相关推荐提供语义匹配（plugincore.SemanticProvider）。
// 开启后公开书籍会在后台建立索引（巡检补齐，章节发布/修改后约 2 分钟更新），向量化记为系统调用。
// 检索分两步：先用书籍向量（分块向量的均值）选出最相关的若干本书，再在这些书的分块中计算相似度，查询开销与全站规模无关。

const (
	cfgSemantic       = "qa_semantic_search"
	entSemanticSearch = "qa.semantic_search" // 能否使用语义搜索（权益；游客按基础值）
	reindexJobType    = "qa.reindex"
	semanticBooks     = 20  // 第一步保留的候选书籍数
	relatedMinScore   = 0.2 // 相关推荐的最低相似度
	searchMinScore    = 0.2
	queryCacheSize    = 512
	reindexDelay      = 2 * time.Minute
)

// BookVector / DocVector 书籍与章节的内容向量（分块向量的归一化均值），用于第一步召回与相关推荐。
type BookVector struct {
	BookID uint   `gorm:"primaryKey"`
	Vector []byte `json:"-"`
}

func (BookVector) TableName() string { return "qa_book_vectors" }

type DocVector struct {
	DocID  uint   `gorm:"primaryKey"`
	BookID uint   `gorm:"index"`
	Vector []byte `json:"-"`
}

func (DocVector) TableName() string { return "qa_doc_vectors" }

func (b *behavior) semanticEnabled() bool {
	return b.core.PluginEnabled(plugins.KeyQA) && b.core.GetSetting(cfgSemantic) == "true"
}

func (b *behavior) semanticAvailable() bool {
	_, embed := b.core.AIStatus()
	return embed && b.semanticEnabled()
}

// —— 向量均值 ——

func meanVector(vs [][]float32) []float32 {
	if len(vs) == 0 {
		return nil
	}
	sum := make([]float64, len(vs[0]))
	for _, v := range vs {
		if len(v) != len(sum) {
			continue
		}
		for i, x := range v {
			sum[i] += float64(x)
		}
	}
	norm := 0.0
	for _, x := range sum {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	out := make([]float32, len(sum))
	if norm == 0 {
		return out
	}
	for i, x := range sum {
		out[i] = float32(x / norm)
	}
	return out
}

// updateCentroids 按当前分块向量重算书籍与章节向量（只含已向量化的分块）。
func (b *behavior) updateCentroids(bookID uint) {
	db := b.core.Gorm()
	var chunks []Chunk
	db.Select("doc_id, embedding").Where("book_id = ? AND embedding IS NOT NULL AND LENGTH(embedding) > 0", bookID).Find(&chunks)
	byDoc := map[uint][][]float32{}
	all := make([][]float32, 0, len(chunks))
	for _, c := range chunks {
		v := decodeVector(c.Embedding)
		byDoc[c.DocID] = append(byDoc[c.DocID], v)
		all = append(all, v)
	}
	_ = db.Transaction(func(tx *gorm.DB) error {
		tx.Where("book_id = ?", bookID).Delete(&DocVector{})
		tx.Where("book_id = ?", bookID).Delete(&BookVector{})
		if len(all) == 0 {
			return nil
		}
		rows := make([]DocVector, 0, len(byDoc))
		for docID, vs := range byDoc {
			rows = append(rows, DocVector{DocID: docID, BookID: bookID, Vector: encodeVector(meanVector(vs))})
		}
		tx.CreateInBatches(&rows, 200)
		return tx.Create(&BookVector{BookID: bookID, Vector: encodeVector(meanVector(all))}).Error
	})
}

// —— 查询向量缓存（相同的搜索词不重复计算）——

var (
	queryMu    sync.Mutex
	queryCache = map[string][]float32{}
)

func (b *behavior) queryVector(ctx context.Context, q string) ([]float32, error) {
	queryMu.Lock()
	if v, ok := queryCache[q]; ok {
		queryMu.Unlock()
		return v, nil
	}
	queryMu.Unlock()
	// 读者搜索的向量化由站点承担，记为系统调用
	vecs, _, err := b.core.AIEmbed(ai.WithCaller(ctx, ai.Caller{Feature: "qa.search"}), []string{q})
	if err != nil || len(vecs) != 1 {
		return nil, err
	}
	queryMu.Lock()
	if len(queryCache) >= queryCacheSize {
		queryCache = map[string][]float32{}
	}
	queryCache[q] = vecs[0]
	queryMu.Unlock()
	return vecs[0], nil
}

type rankedID struct {
	id    uint
	score float64
}

func rank(items []rankedID) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
}

// rankBooks 书籍向量与 v 的相似度排名（只含 u 可读的书籍；exclude 返回 true 的跳过），最多 n 本。
func (b *behavior) rankBooks(u *models.User, v []float32, n int, exclude func(*models.Book) bool) []rankedID {
	db := b.core.Gorm()
	var rows []BookVector
	db.Find(&rows)
	ranked := make([]rankedID, 0, len(rows))
	for _, r := range rows {
		ranked = append(ranked, rankedID{id: r.BookID, score: cosine(v, decodeVector(r.Vector))})
	}
	rank(ranked)
	out := []rankedID{}
	for _, r := range ranked {
		if len(out) >= n {
			break
		}
		var book models.Book
		if db.First(&book, r.id).Error != nil || !b.core.CanReadBook(u, &book) || (exclude != nil && exclude(&book)) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// semanticSearch 与 query 语义相关的小节（每章取最相关的一处）。
func (b *behavior) semanticSearch(ctx context.Context, u *models.User, query string, bookID uint, limit int) ([]plugincore.SemanticHit, error) {
	qv, err := b.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	var bookIDs []uint
	if bookID != 0 {
		bookIDs = []uint{bookID}
	} else {
		for _, r := range b.rankBooks(u, qv, semanticBooks, nil) {
			bookIDs = append(bookIDs, r.id)
		}
	}
	if len(bookIDs) == 0 {
		return nil, nil
	}
	db := b.core.Gorm()
	best := map[uint]plugincore.SemanticHit{}
	for _, id := range bookIDs {
		var book models.Book
		if db.First(&book, id).Error != nil {
			continue
		}
		chunks := b.loadChunks(u, &book) // 已按内容门禁过滤
		for _, c := range chunks {
			if len(c.Embedding) == 0 {
				continue
			}
			s := cosine(qv, decodeVector(c.Embedding))
			if s < searchMinScore {
				continue
			}
			if cur, ok := best[c.DocID]; !ok || s > cur.Score {
				best[c.DocID] = plugincore.SemanticHit{BookID: c.BookID, DocID: c.DocID, Heading: c.Heading, Anchor: c.Anchor, Snippet: c.Content, Score: s}
			}
		}
	}
	hits := make([]plugincore.SemanticHit, 0, len(best))
	for _, h := range best {
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// sameWork 同一作品的其他语言/版本不作为「相关书籍」推荐。
func sameWork(a, b *models.Book) bool {
	return a.ID == b.ID || (a.TransGroup != "" && a.TransGroup == b.TransGroup) || (a.VersionGroup != "" && a.VersionGroup == b.VersionGroup)
}

func (b *behavior) relatedBooks(u *models.User, book *models.Book, limit int) []uint {
	var bv BookVector
	if b.core.Gorm().First(&bv, book.ID).Error != nil {
		return nil
	}
	out := []uint{}
	for _, r := range b.rankBooks(u, decodeVector(bv.Vector), limit, func(other *models.Book) bool { return sameWork(book, other) }) {
		if r.score >= relatedMinScore {
			out = append(out, r.id)
		}
	}
	return out
}

// relatedDocs 与章节相近的其他章节：先选出相关的书（含本书），再比较这些书中的章节向量。
func (b *behavior) relatedDocs(u *models.User, doc *models.Document, limit int) []uint {
	db := b.core.Gorm()
	var dv DocVector
	if db.First(&dv, doc.ID).Error != nil {
		return nil
	}
	v := decodeVector(dv.Vector)
	bookIDs := []uint{doc.BookID}
	for _, r := range b.rankBooks(u, v, semanticBooks, func(other *models.Book) bool { return other.ID == doc.BookID }) {
		bookIDs = append(bookIDs, r.id)
	}
	var rows []DocVector
	db.Where("book_id IN ? AND doc_id <> ?", bookIDs, doc.ID).Find(&rows)
	ranked := make([]rankedID, 0, len(rows))
	for _, r := range rows {
		if s := cosine(v, decodeVector(r.Vector)); s >= relatedMinScore {
			ranked = append(ranked, rankedID{id: r.DocID, score: s})
		}
	}
	rank(ranked)
	out := []uint{}
	for i := 0; i < len(ranked) && len(out) < limit; i++ {
		out = append(out, ranked[i].id)
	}
	return out
}

// —— 全站索引 ——

// visibleBookIDs 对外公开的书籍（语义检索只为这些书建立索引）。
func (b *behavior) visibleBookIDs() []uint {
	var ids []uint
	b.core.Gorm().Model(&models.Book{}).Where("is_public = ? AND status IN ?", true, b.core.PubliclyReadableBookStatuses()).Pluck("id", &ids)
	return ids
}

// enqueueStaleIndexes 为索引缺失或内容已变化的公开书籍排队重建（返回排队数）。
func (b *behavior) enqueueStaleIndexes(ctx context.Context) int {
	q := b.core.JobQueue()
	if q == nil {
		return 0
	}
	db := b.core.Gorm()
	n := 0
	for _, id := range b.visibleBookIDs() {
		var state IndexState
		if db.First(&state, id).Error == nil && state.ContentHash == contentHash(publishedDocs(db, id, false)) && state.Embedded == state.Chunks {
			var has int64
			db.Model(&BookVector{}).Where("book_id = ?", id).Count(&has)
			if has > 0 || state.Chunks == 0 {
				continue
			}
		}
		if _, err := q.Enqueue(ctx, reindexJobType, map[string]any{"book_id": id}, 3); err == nil {
			n++
		}
	}
	return n
}

// runReindexJob 重建一本书的索引、计算向量并更新书籍/章节向量。
func (b *behavior) runReindexJob(ctx context.Context, raw json.RawMessage) error {
	var job struct {
		BookID uint `json:"book_id"`
	}
	if json.Unmarshal(raw, &job) != nil || job.BookID == 0 {
		return nil
	}
	if _, err := b.ensureIndex(ctx, job.BookID); err != nil {
		return err
	}
	return b.embedPending(ai.WithCaller(ctx, ai.Caller{Feature: "qa.index", RefType: "book", RefID: job.BookID}), job.BookID)
}

var (
	reindexMu     sync.Mutex
	reindexTimers = map[uint]*time.Timer{}
)

// scheduleReindex 公开书籍的章节发布或修改后延迟重建（连续修改只重建一次）。
func (b *behavior) scheduleReindex(book *models.Book) {
	if !b.semanticEnabled() || !book.IsPublic {
		return
	}
	id := book.ID
	reindexMu.Lock()
	defer reindexMu.Unlock()
	if t, ok := reindexTimers[id]; ok {
		t.Stop()
	}
	reindexTimers[id] = time.AfterFunc(reindexDelay, func() {
		reindexMu.Lock()
		delete(reindexTimers, id)
		reindexMu.Unlock()
		if q := b.core.JobQueue(); q != nil && b.semanticEnabled() {
			_, _ = q.Enqueue(context.Background(), reindexJobType, map[string]any{"book_id": id}, 3)
		}
	})
}

// semanticStats 管理页展示：公开书籍数与已建立语义索引的书籍数。
func (b *behavior) semanticStats() map[string]int64 {
	var indexed int64
	b.core.Gorm().Model(&BookVector{}).Where("book_id IN ?", append(b.visibleBookIDs(), 0)).Count(&indexed)
	return map[string]int64{"public_books": int64(len(b.visibleBookIDs())), "indexed_books": indexed}
}

func registerSemantic() {
	plugincore.RegisterSemanticProvider(plugincore.SemanticProvider{
		Available: func(core plugincore.Core) bool { return (&behavior{core: core}).semanticAvailable() },
		Allowed: func(core plugincore.Core, u *models.User) bool {
			return plugincore.EntitlementValue(core, u, entSemanticSearch) > 0
		},
		Search: func(ctx context.Context, core plugincore.Core, u *models.User, query string, bookID uint, limit int) ([]plugincore.SemanticHit, error) {
			return (&behavior{core: core}).semanticSearch(ctx, u, query, bookID, limit)
		},
		RelatedBooks: func(_ context.Context, core plugincore.Core, u *models.User, book *models.Book, limit int) []uint {
			return (&behavior{core: core}).relatedBooks(u, book, limit)
		},
		RelatedDocs: func(_ context.Context, core plugincore.Core, u *models.User, doc *models.Document, limit int) []uint {
			return (&behavior{core: core}).relatedDocs(u, doc, limit)
		},
	})
	plugincore.RegisterJob(reindexJobType, func(core plugincore.Core) func(ctx context.Context, raw json.RawMessage) error {
		return (&behavior{core: core}).runReindexJob
	})
	onChange := func(core plugincore.Core, book *models.Book, doc *models.Document) {
		if doc.Status == "published" {
			(&behavior{core: core}).scheduleReindex(book)
		}
	}
	plugincore.OnChapterPublished(onChange)
	plugincore.OnChapterContentChanged(onChange)
}
