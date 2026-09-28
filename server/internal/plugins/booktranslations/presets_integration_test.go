package booktranslations_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"knowforge/server/internal/models"
)

// 新建译本预设：默认访问路径为「原书路径-语言代码」；管理员可为语言设置书名与访问路径模板，新建译本表单据此预填；
// 作者可改写书名与访问路径，访问路径格式无效或已被占用时拒绝；预设路径被占用时自动追加数字后缀。
func TestTranslationPresets(t *testing.T) {
	fake := &fakeAI{}
	aiServer := httptest.NewServer(fake.handler())
	t.Cleanup(aiServer.Close)
	e := newTestEnv(t, aiServer.URL)
	author := e.user(t, "preset-author")
	_, created := e.as(t, author, http.MethodPost, "/api/v1/books", `{"title":"存储原理","slug":"storage","status":"published","is_public":true}`)
	bookID := uint(num(data(created)["id"]))
	e.doc(t, author, bookID, `{"title":"第一章","slug":"intro","content":"内容。","status":"published"}`)
	base := fmt.Sprintf("/api/v1/books/%d/ai-translate", bookID)
	preset := func(code, label string) map[string]any {
		_, p := e.as(t, author, http.MethodGet, base+"/preset?lang="+url.QueryEscape(code)+"&label="+url.QueryEscape(label), "")
		return data(p)
	}

	// 默认：书名留空（由 AI 翻译），访问路径为 原书路径-语言代码
	if p := preset("zh-TW", "繁體中文"); p["title"] != "" || p["slug"] != "storage-zh-tw" {
		t.Fatalf("默认预设异常: %v", p)
	}
	// 管理员为简体中文设置模板；未单独设置的项沿用默认
	if status, p := e.req(t, e.token, http.MethodPut, "/api/v1/admin/book-translations/presets",
		`{"default":{"title":"","slug":"{slug}-{code}"},"languages":{"zh-CN":{"title":"{title} {language}","slug":"{slug}-chinese"},"ja":{"title":"{title}（日本語版）"}}}`); status != http.StatusOK {
		t.Fatalf("保存预设失败: %d %v", status, p)
	}
	if status, _ := e.as(t, author, http.MethodPut, "/api/v1/admin/book-translations/presets", `{"default":{}}`); status != http.StatusForbidden {
		t.Fatalf("非管理员不能修改预设: %d", status)
	}
	if p := preset("zh-CN", "简体中文"); p["title"] != "存储原理 简体中文" || p["slug"] != "storage-chinese" {
		t.Fatalf("简体中文预设异常: %v", p)
	}
	if p := preset("ja", "日本語"); p["title"] != "存储原理（日本語版）" || p["slug"] != "storage-ja" {
		t.Fatalf("日语应沿用默认访问路径模板: %v", p)
	}

	// 作者指定的访问路径：格式无效、已被占用时拒绝
	for body, want := range map[string]int{
		`{"target_lang":"zh-CN","target_label":"简体中文","slug":"Bad Slug"}`: http.StatusBadRequest,
		`{"target_lang":"zh-CN","target_label":"简体中文","slug":"storage"}`:  http.StatusConflict,
	} {
		if status, p := e.as(t, author, http.MethodPost, base+"/jobs", body); status != want {
			t.Fatalf("%s 应返回 %d，实际 %d %v", body, want, status, p)
		}
	}
	// 不传书名与访问路径：按预设
	_, p := e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"zh-CN","target_label":"简体中文"}`)
	done := e.wait(t, author, num(jobOf(data(p))["id"]))
	var zh models.Book
	e.db.First(&zh, uint(num(jobOf(done)["target_book_id"])))
	if zh.Title != "存储原理 简体中文" || zh.Slug != "storage-chinese" {
		t.Fatalf("按预设新建译本异常: %q %q", zh.Title, zh.Slug)
	}
	// 预设路径已被占用：预填追加数字后缀
	if p := preset("zh-CN", "简体中文"); p["slug"] != "storage-chinese-2" {
		t.Fatalf("预设路径被占用时应追加后缀: %v", p)
	}
	// 作者改写：书名留空由 AI 翻译，自定义访问路径
	_, p = e.as(t, author, http.MethodPost, base+"/jobs", `{"target_lang":"en","target_label":"English","title":"","slug":"storage-guide-en"}`)
	done = e.wait(t, author, num(jobOf(data(p))["id"]))
	var en models.Book
	e.db.First(&en, uint(num(jobOf(done)["target_book_id"])))
	if en.Slug != "storage-guide-en" || en.Title != "EN:存储原理" {
		t.Fatalf("自定义访问路径或 AI 书名异常: %q %q", en.Title, en.Slug)
	}
}
