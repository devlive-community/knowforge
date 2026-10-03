package contentcollect

import (
	"net/url"
	"strings"
	"testing"
)

func TestCollectRulesCompileAndPostProcess(t *testing.T) {
	docs, _ := url.Parse("https://docs.example.com/guide")
	other, _ := url.Parse("https://other.org/x")
	r := collectRules{Builtin: map[string]bool{rulePageTOC: false}, Custom: []customRule{
		{Name: "去掉编辑提示", Enabled: true, Type: customRemoveLine, Pattern: `^Edit this page`},
		{Name: "只对 example.com", Enabled: true, Type: customReplace, Pattern: `Foo`, Replacement: "Bar", Hosts: "example.com"},
		{Name: "停用的规则", Enabled: false, Type: customRemoveLine, Pattern: `.*`},
		{Name: "反馈区", Enabled: true, Type: customRemoveElement, Pattern: "div.feedback, [data-testid=rating]"},
	}}
	rs := r.compile(docs)
	if rs.toc || !rs.layout || !rs.permalinks || len(rs.removeLines) != 1 || len(rs.replaces) != 1 || len(rs.selectors) != 2 {
		t.Fatalf("编译结果异常: %+v", rs)
	}
	if len(r.compile(other).replaces) != 0 {
		t.Fatal("限定站点的规则不应对其他站点生效")
	}
	md := "# 标题\n\n##\n\n#\n\n正文 Foo\n\nEdit this page on GitHub\n\n```bash\n#\n# 注释\nEdit this page\n```\n\n¶"
	got := rs.postProcess(md)
	want := "# 标题\n\n正文 Bar\n\n```bash\n#\n# 注释\nEdit this page\n```"
	if got != want {
		t.Fatalf("后处理结果:\n%q\nwant:\n%q", got, want)
	}
	// 关闭空标题规则后保留
	off := collectRules{Builtin: map[string]bool{ruleEmptyHeadings: false}}.compile(docs)
	if !strings.Contains(off.postProcess("正文\n\n##\n\n结尾"), "##") {
		t.Fatal("关闭空标题规则后不应删除")
	}
}

func TestSelectors(t *testing.T) {
	if _, err := parseSelectors("div .x"); err == nil {
		t.Fatal("不支持层级选择器")
	}
	if _, err := parseSelectors("*"); err == nil {
		t.Fatal("不能匹配所有元素")
	}
	sels, err := parseSelectors(`a.edit.link#e1[aria-label="Edit"], [data-x]`)
	if err != nil || len(sels) != 2 || sels[0].tag != "a" || sels[0].id != "e1" || len(sels[0].classes) != 2 || sels[0].attrs[0].value != "Edit" || sels[1].attrs[0].name != "data-x" || sels[1].attrs[0].hasValue {
		t.Fatalf("解析异常: %+v %v", sels, err)
	}
}

func TestCollectRulesValidateAndRemoveElement(t *testing.T) {
	bad := collectRules{Custom: []customRule{{Name: "坏正则", Type: customRemoveLine, Pattern: "(", Enabled: true}}}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "坏正则") {
		t.Fatalf("应拒绝无效正则: %v", err)
	}
	if err := (&collectRules{Custom: []customRule{{Type: "x", Pattern: "a"}}}).validate(); err == nil {
		t.Fatal("应拒绝未知类型")
	}
	pageURL, _ := url.Parse("https://8.8.8.8/doc")
	r := collectRules{Custom: []customRule{{Name: "反馈", Enabled: true, Type: customRemoveElement, Pattern: "div.rate-box"}}}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	article, err := extractWebArticleWith(webPage{FinalURL: pageURL, HTML: `<html><body><main><article>
<h1>文档标题</h1><p>这是正文第一段，内容足够长以便识别为正文区域。</p>
<div class="rate-box">给本页打分：好 / 一般 / 差</div>
<p>第二段正文。</p></article></main></body></html>`}, r.compile(pageURL))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(article.Markdown, "给本页打分") || !strings.Contains(article.Markdown, "第二段正文") {
		t.Fatalf("自定义删除元素规则未生效: %q", article.Markdown)
	}
}

// 文档站装饰规则去掉正文中的按钮（如「Copy page」），关闭后保留。
func TestDecorationsRemoveButtons(t *testing.T) {
	pageURL, _ := url.Parse("https://8.8.8.8/doc")
	page := webPage{FinalURL: pageURL, HTML: `<html><body><main><article><h1>标题</h1><button>Copy page</button>
<p>这是正文第一段，内容足够长以便识别为正文区域。</p></article></main></body></html>`}
	on, err := extractWebArticleWith(page, defaultRules)
	if err != nil || strings.Contains(on.Markdown, "Copy page") {
		t.Fatalf("应去掉按钮: %q %v", on.Markdown, err)
	}
	off, _ := extractWebArticleWith(page, collectRules{Builtin: map[string]bool{ruleDecorations: false}}.compile(pageURL))
	if !strings.Contains(off.Markdown, "Copy page") {
		t.Fatalf("关闭规则后应保留: %q", off.Markdown)
	}
}
