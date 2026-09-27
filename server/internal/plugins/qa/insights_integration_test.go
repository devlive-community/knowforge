package qa_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins/qa"
)

// 作者洞察：相近问题归类、内容缺口、问题最多的章节、未回答的社区提问、作者自己的提问不计入、隐私开关、权益与每周摘要。
func TestAuthorInsights(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author, r1, r2 := e.user(t, "author"), e.user(t, "reader1"), e.user(t, "reader2")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","status":"published","is_public":true}`)
	bookID := uint(data(created)["id"].(float64))
	_, d := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), `{"title":"第一章 缓存","content":"缓存可以加速读取。","status":"published"}`)
	docID := uint(data(d)["id"].(float64))
	base := fmt.Sprintf("/api/v1/qa/books/%d/insights", bookID)

	cited := fmt.Sprintf(`[{"n":1,"doc_id":%d,"doc_slug":"x","doc_title":"第一章 缓存"}]`, docID)
	ask := func(u *models.User, question, citations string, docID uint) {
		a := qa.Ask{BookID: bookID, UserID: u.ID, DocID: docID, Mode: "rag", Question: question, Status: "done", Answer: "回答", Citations: citations, Trace: "[]", Calls: 1}
		if err := e.db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	ask(r1, "缓存有什么用？", cited, docID)
	ask(r2, "缓存为什么能加速", cited, 0)
	ask(r1, "如何部署集群？", "[]", 0)      // 书中答不上来
	ask(author, "缓存的作者自测", cited, 0) // 作者自己的提问不计入
	if status, p := e.as(t, r2, http.MethodPost, fmt.Sprintf("/api/v1/qa/books/%d/questions", bookID), `{"title":"集群怎么部署","body":"想了解部署步骤"}`); status != http.StatusOK {
		t.Fatalf("社区提问失败: %d %v", status, p)
	}

	// 巡检补算问题向量（按「缓存」「索引」出现次数的假向量）
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	var missing int64
	e.db.Model(&qa.Ask{}).Where("book_id = ? AND (q_embedding IS NULL OR LENGTH(q_embedding) = 0)", bookID).Count(&missing)
	if missing != 0 {
		t.Fatalf("问题向量应已补算，缺 %d", missing)
	}

	if status, _ := e.as(t, r1, http.MethodGet, base, ""); status != http.StatusForbidden {
		t.Fatalf("读者不能查看洞察: %d", status)
	}
	status, p := e.as(t, author, http.MethodGet, base+"?days=30", "")
	if status != http.StatusOK {
		t.Fatalf("读取洞察失败: %d %v", status, p)
	}
	stats := data(p)["stats"].(map[string]any)
	if stats["ai_asks"].(float64) != 3 || stats["askers"].(float64) != 2 || stats["gaps"].(float64) != 1 || stats["community"].(float64) != 1 || stats["open"].(float64) != 1 {
		t.Fatalf("统计异常: %v", stats)
	}
	topics := data(p)["topics"].([]any)
	var top map[string]any
	for _, tp := range topics {
		if m := tp.(map[string]any); strings.Contains(m["question"].(string), "缓存") {
			top = m
		}
	}
	if len(topics) != 2 || top == nil || top["count"].(float64) != 2 {
		t.Fatalf("缓存相关的两个问题应归为一类: %v", topics)
	}
	if ch := top["chapters"].([]any); len(ch) != 1 || uint(ch[0].(map[string]any)["id"].(float64)) != docID || ch[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("主题关联章节异常: %v", top["chapters"])
	}
	for _, s := range top["samples"].([]any) {
		if strings.Contains(s.(string), "作者自测") {
			t.Fatalf("作者自己的提问不应计入: %v", top["samples"])
		}
	}
	gaps := data(p)["gaps"].([]any)
	if len(gaps) != 1 || gaps[0].(map[string]any)["gaps"].(float64) != 1 || gaps[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("内容缺口应把答不上来的 AI 提问与相近的社区提问归在一起: %v", gaps)
	}
	if open := data(p)["unanswered"].([]any); len(open) != 1 || open[0].(map[string]any)["title"] != "集群怎么部署" {
		t.Fatalf("未回答的社区提问异常: %v", open)
	}
	if chapters := data(p)["chapters"].([]any); len(chapters) != 1 {
		t.Fatalf("问题最多的章节异常: %v", chapters)
	}

	// 时间范围：7 天前的提问不计入最近 7 天
	e.db.Model(&qa.Ask{}).Where("question = ?", "如何部署集群？").Update("created_at", time.Now().AddDate(0, 0, -10))
	_, p = e.as(t, author, http.MethodGet, base+"?days=7", "")
	if data(p)["stats"].(map[string]any)["gaps"].(float64) != 0 {
		t.Fatalf("7 天范围不应包含 10 天前的提问: %v", data(p)["stats"])
	}

	// 每周摘要：有新提问时通知作者一次
	var notes []models.Notification
	e.db.Where("user_id = ? AND title LIKE ?", author.ID, "%本周读者提问%").Find(&notes)
	if len(notes) != 1 {
		t.Fatalf("应发送一次每周摘要: %d", len(notes))
	}
	plugincore.FireJobQueueSweep(e.app, e.app.Jobs)
	e.db.Where("user_id = ? AND title LIKE ?", author.ID, "%本周读者提问%").Find(&notes)
	if len(notes) != 1 {
		t.Fatalf("同一周不应重复发送: %d", len(notes))
	}

	// 关闭 AI 提问汇总：只统计社区提问
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/qa/settings", `{"insights_asks":false}`)
	_, p = e.as(t, author, http.MethodGet, base, "")
	if data(p)["include_asks"] != false || data(p)["stats"].(map[string]any)["ai_asks"] != nil || len(data(p)["topics"].([]any)) != 1 {
		t.Fatalf("关闭后不应包含 AI 提问: %v", data(p))
	}
	if _, st := e.as(t, r1, http.MethodGet, fmt.Sprintf("/api/v1/qa/books/%d/status", bookID), ""); data(st)["insights_share"] != false {
		t.Fatalf("读者端应得知提问不再汇总: %v", st)
	}

	// 权益关闭后不可查看
	e.req(t, e.token, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"qa.insights":0}}`)
	if status, p := e.as(t, author, http.MethodGet, base, ""); status != http.StatusForbidden || !strings.Contains(p["message"].(string), "会员") {
		t.Fatalf("权益关闭后应不可用: %d %v", status, p)
	}
}
