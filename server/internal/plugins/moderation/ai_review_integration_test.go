package moderation_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins/moderation"
)

// AI 辅助审核（假模型按内容中的标记给出结论）：安全时自动通过、置信度不足与违规时保持待审核、仅参考模式、
// 复查自动通过的内容、作者不可见、重新复核、事件流、计量与关闭。
type fakeModerator struct {
	mu      sync.Mutex
	prompts []string
}

func (f *fakeModerator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(raw, &req)
		user := req.Messages[len(req.Messages)-1].Content
		f.mu.Lock()
		f.prompts = append(f.prompts, user)
		f.mu.Unlock()
		verdict := `{"verdict":"safe","confidence":0.99,"categories":[],"reason":"正常内容"}`
		switch {
		case strings.Contains(user, "炸药制作"):
			verdict = `{"verdict":"violation","confidence":0.95,"categories":["暴力恐怖"],"reason":"包含危险物品制作方法"}`
		case strings.Contains(user, "拿不准"):
			verdict = `{"verdict":"safe","confidence":0.6,"categories":[],"reason":"大概率正常"}`
		case strings.Contains(user, "误判样例"):
			verdict = `{"verdict":"safe","confidence":0.97,"categories":[],"reason":"敏感词出现在正常语境中"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake-mod", "usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 10},
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "结论：" + verdict}}}})
	})
	return mux
}

func (f *fakeModerator) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[len(f.prompts)-1]
}

func (e *testEnv) runJobs(t *testing.T) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ran, err := e.app.Jobs.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("任务失败: %v", err)
		}
		if !ran {
			return
		}
	}
}

func TestAIAssistedModeration(t *testing.T) {
	fake := &fakeModerator{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t)
	if status, p := e.admin(t, http.MethodPut, "/api/v1/admin/ai", fmt.Sprintf(`{"provider":"openai","base_url":%q,"api_key":"k","model":"fake"}`, aiServer.URL+"/v1")); status != http.StatusOK {
		t.Fatalf("配置 AI 失败: %d %v", status, p)
	}
	e.admin(t, http.MethodPost, "/api/v1/admin/moderation/words", `{"words":"敏感词","category":"测试"}`)
	author := &models.User{Username: "writer", Email: "writer@test.local", Role: "user", IsActive: true, EmailVerified: true}
	e.db.Create(author)
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"审核之书"}`)
	bookID := uint(data(created)["id"].(float64))
	newDoc := func(content string) uint {
		t.Helper()
		status, p := e.as(t, author, http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), fmt.Sprintf(`{"title":"章节","content":%q,"status":"published"}`, content))
		if status != http.StatusOK {
			t.Fatalf("创建章节失败: %d %v", status, p)
		}
		return uint(data(p)["id"].(float64))
	}

	// 设置校验
	if status, _ := e.admin(t, http.MethodPut, "/api/v1/admin/moderation/ai-settings", `{"mode":"always"}`); status != http.StatusBadRequest {
		t.Fatalf("不支持的模式应被拒绝: %d", status)
	}
	if status, _ := e.admin(t, http.MethodPut, "/api/v1/admin/moderation/ai-settings", `{"min_confidence":0.3}`); status != http.StatusBadRequest {
		t.Fatalf("置信度范围应被校验: %d", status)
	}
	// 关闭时不排队
	off := newDoc("关闭时 敏感词")
	e.runJobs(t)
	if c := e.caseFor("document", off); c.AIStatus != "" || c.Status != moderation.StatusPending {
		t.Fatalf("未开启时不应复核: %+v", c)
	}

	status, p := e.admin(t, http.MethodPut, "/api/v1/admin/moderation/ai-settings", `{"mode":"auto_approve","min_confidence":0.9,"screen_passed":true,"policy":"禁止讨论竞品"}`)
	if status != http.StatusOK || data(p)["settings"].(map[string]any)["mode"] != "auto_approve" || data(p)["ai_available"] != true {
		t.Fatalf("保存 AI 设置失败: %d %v", status, p)
	}

	// 事件流：复核过程推送记录状态
	var adminUser models.User
	e.db.Where("username = ?", "mod-admin").First(&adminUser)
	adminToken, _ := auth.GenerateToken(e.app.Config.Secret, adminUser.ID, adminUser.Username, adminUser.Role)
	r, _ := http.NewRequest(http.MethodGet, e.server.URL+"/api/v1/admin/moderation/stream", nil)
	r.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := (&http.Client{}).Do(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("订阅失败: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var item struct {
					Case moderation.Case `json:"case"`
				}
				if json.Unmarshal([]byte(d), &item) == nil && item.Case.ID != 0 {
					events <- item.Case.AIStatus
				}
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// 1. 敏感词误判：模型判定安全且置信度足够 → 自动通过并发布
	misfire := newDoc("这是一个误判样例，文中提到了敏感词")
	if e.docStatus(misfire) != "draft" {
		t.Fatalf("先按敏感词拦截: %s", e.docStatus(misfire))
	}
	e.runJobs(t)
	c := e.caseFor("document", misfire)
	if e.docStatus(misfire) != "published" || c.Status != moderation.StatusApproved || c.ReviewerID != 0 || !strings.HasPrefix(c.ReviewNote, "AI 复核通过") ||
		c.AIVerdict != "safe" || c.AIConfidence != 0.97 || c.AIModel != "fake-mod" {
		t.Fatalf("应由 AI 自动通过: %s %+v", e.docStatus(misfire), c)
	}
	if prompt := fake.last(); !strings.Contains(prompt, "禁止讨论竞品") || !strings.Contains(prompt, "「敏感词」") || !strings.Contains(prompt, "误判样例") {
		t.Fatalf("提示词应包含补充规则、命中与内容: %s", prompt)
	}
	if e.notices(author.ID, "notify.moderation.approved") != 1 {
		t.Fatalf("自动通过后应通知作者")
	}
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !seen["done"] {
		select {
		case s := <-events:
			seen[s] = true
		case <-deadline:
			t.Fatalf("未收到复核完成事件: %v", seen)
		}
	}
	if !seen["queued"] || !seen["reviewing"] || !seen["done"] {
		t.Fatalf("事件流应推送复核过程: %v", seen)
	}

	// 2. 置信度不足：保持待审核，结论供审核员参考
	unsure := newDoc("拿不准 敏感词")
	e.runJobs(t)
	if c := e.caseFor("document", unsure); c.Status != moderation.StatusPending || c.AIVerdict != "safe" || c.AIConfidence != 0.6 || e.docStatus(unsure) != "draft" {
		t.Fatalf("置信度不足应保持待审核: %+v", c)
	}
	// 3. 判定违规：保持待审核
	bad := newDoc("敏感词 炸药制作")
	e.runJobs(t)
	if c := e.caseFor("document", bad); c.Status != moderation.StatusPending || c.AIVerdict != "violation" || len(c.AICategories) != 1 || c.AIReason == "" {
		t.Fatalf("违规应保持待审核: %+v", c)
	}
	// 4. 词典没拦住的内容：自动通过后复查，标记并通知管理员（不自动撤回）
	sneaky := newDoc("一篇关于炸药制作的教程")
	e.runJobs(t)
	if c := e.caseFor("document", sneaky); c.Status != moderation.StatusAutoPassed || c.AIVerdict != "violation" || e.docStatus(sneaky) != "published" {
		t.Fatalf("复查应标记但不撤回: %+v", c)
	}
	if e.notices(adminUser.ID, "notify.moderation.aiFlagged") != 1 {
		t.Fatalf("应通知管理员复审被标记的内容")
	}
	_, list := e.admin(t, http.MethodGet, "/api/v1/admin/moderation/cases?ai=flagged", "")
	if data(list)["total"].(float64) != 2 || data(list)["ai_flagged"].(float64) != 2 || data(list)["ai_active"] != true {
		t.Fatalf("AI 标记筛选异常: %v", data(list))
	}

	// 作者看不到 AI 结论
	_, mine := e.as(t, author, http.MethodGet, "/api/v1/users/me/moderation-cases", "")
	for _, it := range data(mine)["items"].([]any) {
		cs := it.(map[string]any)["case"].(map[string]any)
		if cs["ai_verdict"] != "" || cs["ai_reason"] != "" {
			t.Fatalf("作者不应看到 AI 结论: %v", cs)
		}
	}

	// 重新复核；已处理的记录不能复核
	badCase := e.caseFor("document", bad)
	if status, p := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/ai-review", badCase.ID), ""); status != http.StatusOK || data(p)["case"].(map[string]any)["ai_status"] != "queued" {
		t.Fatalf("重新复核失败: %d %v", status, p)
	}
	e.runJobs(t)
	if c := e.caseFor("document", bad); c.AIStatus != "done" {
		t.Fatalf("重新复核后应完成: %+v", c)
	}
	misfireCase := e.caseFor("document", misfire)
	if status, _ := e.admin(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/moderation/cases/%d/ai-review", misfireCase.ID), ""); status != http.StatusConflict {
		t.Fatalf("已处理的记录不能复核: %d", status)
	}

	// 仅参考模式：安全也不自动通过
	e.admin(t, http.MethodPut, "/api/v1/admin/moderation/ai-settings", `{"mode":"advise"}`)
	advised := newDoc("又一个误判样例 敏感词")
	e.runJobs(t)
	if c := e.caseFor("document", advised); c.Status != moderation.StatusPending || c.AIVerdict != "safe" {
		t.Fatalf("仅参考模式不应自动通过: %+v", c)
	}

	// 计量：系统调用
	var logs []models.AIUsageLog
	e.db.Where("feature = ?", "moderation.ai").Find(&logs)
	if len(logs) != 6 || logs[0].UserID != 0 || logs[0].RefType != "moderation_case" {
		t.Fatalf("AI 复核应记为系统调用: %d %+v", len(logs), logs)
	}
}
