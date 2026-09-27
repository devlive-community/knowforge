package qa_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/qa"
)

// 全站语义检索（假嵌入服务按「缓存」「索引」出现次数构造向量）：开关、公开书籍索引、搜索、相关书籍与章节、巡检更新、查询向量缓存。
func TestSemanticSearchAndRelated(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, reader := e.user(t, "author"), e.user(t, "reader")

	book := func(title, extra string) (uint, string) {
		_, p := e.as(t, author, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"status":"published","is_public":true%s}`, title, extra))
		return uint(data(p)["id"].(float64)), data(p)["slug"].(string)
	}
	doc := func(bookID uint, title, content string) uint {
		_, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":%q,"content":%q,"status":"published"}`, title, content))
		return uint(data(p)["id"].(float64))
	}
	cacheBook, _ := book("缓存原理", `,"trans_group":"cache"`)
	cacheDoc := doc(cacheBook, "缓存篇", "## 读缓存\n\n缓存 缓存 缓存 可以加速读取。")
	cacheTrans, _ := book("Cache Principles", `,"trans_group":"cache"`) // 同一作品的译本：不作为相关书籍
	doc(cacheTrans, "Cache", "缓存 缓存 缓存 translated")
	cacheMore, _ := book("缓存进阶", "")
	moreDoc := doc(cacheMore, "多级缓存", "缓存 缓存 多级缓存设计。")
	indexBook, indexSlug := book("索引原理", "")
	indexDoc := doc(indexBook, "索引篇", "索引 索引 索引 帮助检索。")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"私有缓存笔记","status":"draft","is_public":false}`)
	privateBook := uint(data(created)["id"].(float64))
	doc(privateBook, "私有", "缓存 缓存 缓存 私密内容。")

	// 未开启：不可用，相关推荐为空
	if _, p := e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=缓存", ""); data(p)["available"] != false {
		t.Fatalf("未开启时应不可用: %v", p)
	}
	if status, _ := e.req(t, e.token, http.MethodPost, "/api/v1/admin/qa/semantic/reindex", ""); status != http.StatusBadRequest {
		t.Fatalf("未开启时不能重建: %d", status)
	}

	// 开启并为公开书籍建立索引（私有书籍不建）
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"semantic_search":true}`); status != http.StatusOK || data(p)["settings"].(map[string]any)["semantic_search"] != true {
		t.Fatalf("开启失败: %d %v", status, p)
	}
	e.req(t, e.token, http.MethodPost, "/api/v1/admin/qa/semantic/reindex", "")
	e.runJobs(t)
	var vectors []qa.BookVector
	e.db.Find(&vectors)
	indexed := map[uint]bool{}
	for _, v := range vectors {
		indexed[v.BookID] = true
	}
	if !indexed[cacheBook] || !indexed[indexBook] || !indexed[cacheMore] || indexed[privateBook] {
		t.Fatalf("应只为公开书籍建立向量: %v", indexed)
	}
	if _, p := e.req(t, e.token, http.MethodGet, "/api/v1/admin/qa/settings", ""); data(p)["semantic"].(map[string]any)["indexed_books"].(float64) != 4 {
		t.Fatalf("索引统计异常: %v", data(p)["semantic"])
	}

	// 搜索：命中缓存相关的章节（含小节锚点），不含私有书籍，也不含无关的索引书
	before := fake.embeds
	_, p := e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=缓存", "")
	items := data(p)["items"].([]any)
	if data(p)["available"] != true || len(items) == 0 {
		t.Fatalf("语义搜索应有结果: %v", p)
	}
	found := map[uint]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		found[uint(m["id"].(float64))] = m
		if uint(m["book_id"].(float64)) == privateBook || uint(m["id"].(float64)) == indexDoc {
			t.Fatalf("不应出现私有或无关内容: %v", m)
		}
	}
	if hit := found[cacheDoc]; hit == nil || hit["heading"] != "读缓存" || hit["anchor"] != "h-1" || hit["book_title"] != "缓存原理" {
		t.Fatalf("缓存章节命中异常: %v", found[cacheDoc])
	}
	// 相同的搜索词复用查询向量
	e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=缓存", "")
	if fake.embeds != before+1 {
		t.Fatalf("相同搜索词应只向量化一次: %d -> %d", before, fake.embeds)
	}
	var searchLog models.AIUsageLog
	if e.db.Where("feature = ?", "qa.search").First(&searchLog).Error != nil || searchLog.UserID != 0 {
		t.Fatalf("搜索的向量化应记为系统调用: %+v", searchLog)
	}
	// 限定在某本书内
	_, p = e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=索引&book="+indexSlug, "")
	for _, it := range data(p)["items"].([]any) {
		if uint(it.(map[string]any)["book_id"].(float64)) != indexBook {
			t.Fatalf("限定书籍后不应出现其他书: %v", it)
		}
	}

	// 相关书籍：内容相近的其他书（不含同一作品的译本、私有书籍与无关书）
	_, p = e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/related", cacheBook), "")
	related := data(p)["items"].([]any)
	if len(related) != 1 || uint(related[0].(map[string]any)["id"].(float64)) != cacheMore {
		t.Fatalf("相关书籍异常: %v", related)
	}
	// 相关章节
	_, p = e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/documents/%d/related", cacheDoc), "")
	relDocs := data(p)["items"].([]any)
	if len(relDocs) == 0 || uint(relDocs[0].(map[string]any)["id"].(float64)) == indexDoc {
		t.Fatalf("相关章节异常: %v", relDocs)
	}
	hasMore := false
	for _, d := range relDocs {
		if uint(d.(map[string]any)["id"].(float64)) == moreDoc {
			hasMore = true
		}
	}
	if !hasMore {
		t.Fatalf("相关章节应包含其他书中的缓存章节: %v", relDocs)
	}

	// 内容修改后由巡检重建：索引书改写为缓存内容后可被搜到
	e.as(t, author, http.MethodPut, fmt.Sprintf("/api/v1/documents/%d", indexDoc), `{"content":"缓存 缓存 缓存 缓存 改写后的内容。"}`)
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	e.runJobs(t)
	_, p = e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=缓存", "")
	updated := false
	for _, it := range data(p)["items"].([]any) {
		if uint(it.(map[string]any)["id"].(float64)) == indexDoc {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("修改后应重新索引: %v", data(p)["items"])
	}

	// 语义搜索为权益：关闭后读者与游客的搜索不可用，相关推荐不受影响，管理员不受限
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"qa.semantic_search":0}}`)
	if _, p := e.as(t, reader, http.MethodGet, "/api/v1/search/semantic?q=缓存", ""); data(p)["available"] != false {
		t.Fatalf("权益关闭后读者不可用: %v", p)
	}
	if _, p := e.req(t, "", http.MethodGet, "/api/v1/search/semantic?q=缓存", ""); data(p)["available"] != false {
		t.Fatalf("权益关闭后游客不可用: %v", p)
	}
	if _, p := e.req(t, e.token, http.MethodGet, "/api/v1/search/semantic?q=缓存", ""); data(p)["available"] != true {
		t.Fatalf("管理员不受限: %v", p)
	}
	if _, p := e.as(t, reader, http.MethodGet, fmt.Sprintf("/api/v1/books/%d/related", cacheBook), ""); len(data(p)["items"].([]any)) == 0 {
		t.Fatalf("相关推荐不受语义搜索权益影响: %v", p)
	}
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"qa.semantic_search":1}}`)

	// 关闭后不可用
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"semantic_search":false}`)
	if _, p := e.req(t, "", http.MethodGet, fmt.Sprintf("/api/v1/books/%d/related", cacheBook), ""); len(data(p)["items"].([]any)) != 0 {
		t.Fatalf("关闭后相关推荐应为空: %v", p)
	}
}
