package qa

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	instances "knowforge/server/internal/cluster"
	"knowforge/server/internal/models"
)

// —— 分块 ——

const (
	maxChunkRunes = 900
	chunkOverlap  = 100
	embedBatch    = 32
	embedMaxRunes = 2000
)

var (
	headingLine  = regexp.MustCompile(`^(#{2,3})\s+(.+)$`)
	mdLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdDecoration = regexp.MustCompile("[*_`~]|\\{#[^}]*\\}|<[^>]+>")
)

func headingText(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return strings.TrimSpace(mdDecoration.ReplaceAllString(s, ""))
}

type section struct {
	heading, anchor string
	body            []string
}

// splitSections 按 H2/H3 切分章节正文；锚点编号规则与阅读页一致（代码块内的 # 不计，h-1、h-2…）。
func splitSections(content string) []section {
	var out []section
	cur := section{}
	inCode, seq := false, 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
		}
		if !inCode {
			if m := headingLine.FindStringSubmatch(line); m != nil {
				out = append(out, cur)
				seq++
				cur = section{heading: headingText(m[2]), anchor: fmt.Sprintf("h-%d", seq)}
				continue
			}
		}
		cur.body = append(cur.body, line)
	}
	return append(out, cur)
}

// splitBody 过长的小节按段落拆分（单段过长时按字数硬切并保留重叠）。
func splitBody(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	if utf8.RuneCountInString(body) <= maxChunkRunes {
		return []string{body}
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, para := range strings.Split(body, "\n\n") {
		if utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(para) > maxChunkRunes {
			flush()
		}
		if r := []rune(para); len(r) > maxChunkRunes {
			for start := 0; start < len(r); start += maxChunkRunes - chunkOverlap {
				end := start + maxChunkRunes
				if end > len(r) {
					end = len(r)
				}
				out = append(out, string(r[start:end]))
				if end == len(r) {
					break
				}
			}
			continue
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(para)
	}
	flush()
	return out
}

// —— 分词（关键词检索）：拉丁文按词，中日韩文字取单字 + 相邻二元组 ——

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func terms(s string) []string {
	var out []string
	var word []rune
	var prev rune
	flush := func() {
		if len(word) > 0 {
			out = append(out, string(word))
			word = word[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case isCJK(r):
			flush()
			out = append(out, string(r))
			if prev != 0 {
				out = append(out, string([]rune{prev, r}))
			}
			prev = r
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
			prev = 0
		default:
			flush()
			prev = 0
		}
	}
	flush()
	return out
}

// —— 向量编码 ——

func encodeVector(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

func decodeVector(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// —— 建索引 ——

var bookLocks sync.Map // 书籍 ID → *sync.Mutex，避免同一本书并发重建

// lockBook 同一本书的索引重建互斥：本实例内用本地锁，多实例之间用集群租约。
func lockBook(ctx context.Context, id uint) (func(), error) {
	m, _ := bookLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	release, err := instances.Lock(ctx, fmt.Sprintf("qa.index:%d", id), 10*time.Minute)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		release()
		mu.Unlock()
	}, nil
}

// publishedDocs 已发布章节（按目录深度优先顺序）；withContent 为 false 时不加载正文（只用于计算摘要）。
func publishedDocs(db *gorm.DB, bookID uint, withContent bool) []models.Document {
	cols := "id, slug, title, parent_id, sort_order, updated_at"
	if withContent {
		cols += ", content"
	}
	var docs []models.Document
	db.Select(cols).Where("book_id = ? AND status = ?", bookID, "published").Find(&docs)
	children := map[uint][]models.Document{}
	for _, d := range docs {
		parent := uint(0)
		if d.ParentID != nil {
			parent = *d.ParentID
		}
		children[parent] = append(children[parent], d)
	}
	for k := range children {
		list := children[k]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].SortOrder != list[j].SortOrder {
				return list[i].SortOrder < list[j].SortOrder
			}
			return list[i].ID < list[j].ID
		})
	}
	var out []models.Document
	var walk func(parent uint)
	walk = func(parent uint) {
		for _, d := range children[parent] {
			out = append(out, d)
			walk(d.ID)
		}
	}
	walk(0)
	return out
}

func contentHash(docs []models.Document) string {
	h := sha256.New()
	for _, d := range docs {
		fmt.Fprintf(h, "%d:%d;", d.ID, d.UpdatedAt.UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func embedKey(docID uint, heading, content string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", docID, heading, content)))
	return hex.EncodeToString(sum[:])
}

// ensureIndex 书籍内容变化时重建分块（未变化的分块保留已算好的向量）；需要时投递向量化任务。
func (b *behavior) ensureIndex(ctx context.Context, bookID uint) (IndexState, error) {
	unlock, err := lockBook(ctx, bookID)
	if err != nil {
		return IndexState{}, err
	}
	defer unlock()
	db := b.core.Gorm()
	hash := contentHash(publishedDocs(db, bookID, false))
	var state IndexState
	if db.First(&state, bookID).Error == nil && state.ContentHash == hash {
		return state, nil
	}
	docs := publishedDocs(db, bookID, true)
	// 复用未变化分块的向量
	var old []Chunk
	db.Select("doc_id, heading, content, embedding").Where("book_id = ? AND embedding IS NOT NULL", bookID).Find(&old)
	reuse := map[string][]byte{}
	for _, c := range old {
		if len(c.Embedding) > 0 {
			reuse[embedKey(c.DocID, c.Heading, c.Content)] = c.Embedding
		}
	}
	var chunks []Chunk
	ordinal := 0
	for _, d := range docs {
		for _, sec := range splitSections(d.Content) {
			for _, part := range splitBody(strings.Join(sec.body, "\n")) {
				ordinal++
				c := Chunk{BookID: bookID, DocID: d.ID, DocSlug: d.Slug, DocTitle: d.Title, Heading: sec.heading, Anchor: sec.anchor, Ordinal: ordinal, Content: part,
					Terms: strings.Join(terms(d.Title+" "+sec.heading+" "+part), " ")}
				c.Embedding = reuse[embedKey(d.ID, sec.heading, part)]
				chunks = append(chunks, c)
			}
		}
	}
	embedded := 0
	for _, c := range chunks {
		if len(c.Embedding) > 0 {
			embedded++
		}
	}
	state = IndexState{BookID: bookID, ContentHash: hash, Chunks: len(chunks), Embedded: embedded, IndexedAt: time.Now()}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("book_id = ?", bookID).Delete(&Chunk{}).Error; err != nil {
			return err
		}
		if len(chunks) > 0 {
			if err := tx.CreateInBatches(&chunks, 200).Error; err != nil {
				return err
			}
		}
		return tx.Save(&state).Error
	})
	if err != nil {
		return state, err
	}
	if embedded == len(chunks) {
		b.updateCentroids(bookID) // 分块有变化但向量都可复用：直接更新书籍/章节向量
	}
	if _, embed := b.core.AIStatus(); embed && embedded < len(chunks) {
		if q := b.core.JobQueue(); q != nil {
			_, _ = q.Enqueue(ctx, indexJobType, map[string]any{"book_id": bookID}, 3)
		}
	}
	return state, nil
}

// runEmbedJob 后台为未向量化的分块计算向量（分批，失败时记录错误并重试）。
func (b *behavior) runEmbedJob(ctx context.Context, raw json.RawMessage) error {
	var job struct {
		BookID uint `json:"book_id"`
	}
	if err := json.Unmarshal(raw, &job); err != nil || job.BookID == 0 {
		return fmt.Errorf("问答索引任务参数无效")
	}
	return b.embedPending(ai.WithCaller(ctx, ai.Caller{Feature: "qa.index", RefType: "book", RefID: job.BookID}), job.BookID)
}

func (b *behavior) embedPending(ctx context.Context, bookID uint) error {
	db := b.core.Gorm()
	for {
		var pending []Chunk
		db.Select("id, doc_title, heading, content").Where("book_id = ? AND (embedding IS NULL OR LENGTH(embedding) = 0)", bookID).Order("id").Limit(embedBatch).Find(&pending)
		if len(pending) == 0 {
			break
		}
		texts := make([]string, len(pending))
		for i, c := range pending {
			texts[i] = embedText(c)
		}
		vecs, _, err := b.core.AIEmbed(ctx, texts)
		if err != nil {
			db.Model(&IndexState{}).Where("book_id = ?", bookID).Update("embed_error", truncate(err.Error(), 500))
			return err
		}
		for i, c := range pending {
			db.Model(&Chunk{}).Where("id = ?", c.ID).Update("embedding", encodeVector(vecs[i]))
		}
	}
	var embedded int64
	db.Model(&Chunk{}).Where("book_id = ? AND embedding IS NOT NULL AND LENGTH(embedding) > 0", bookID).Count(&embedded)
	db.Model(&IndexState{}).Where("book_id = ?", bookID).Updates(map[string]any{"embedded": embedded, "embed_error": ""})
	b.updateCentroids(bookID)
	return nil
}

func embedText(c Chunk) string {
	text := c.DocTitle
	if c.Heading != "" {
		text += " › " + c.Heading
	}
	return truncate(text+"\n"+c.Content, embedMaxRunes)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
