package mdmask

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 掩码后原样还原必须与原文完全一致（不翻译时往返无损）。
func TestMaskRoundTrip(t *testing.T) {
	guide, err := os.ReadFile("testdata/syntax-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{string(guide), "", "纯文本", "---\n分隔线开头\n---\n", "<!-- icon: a\n多行注释 -->\n正文"} {
		m := Mask(src, Options{})
		out, err := m.Restore(m.Text)
		if err != nil || out != src {
			t.Fatalf("往返不一致: %v\n%q\n----\n%q", err, src, out)
		}
	}
}

// 交给翻译的文本中不应出现任何语法关键字；读者可见的文字要保留。
func TestMaskHidesSyntax(t *testing.T) {
	src := strings.Join([]string{
		"---",
		"title: 常见用例指南",
		"url: https://example.com/docs",
		"description: \"探索生产指南\"",
		"icon: book",
		"---",
		"<!-- icon: database -->",
		"",
		"[toc]",
		"",
		"[children]",
		"",
		"## 概览 :rocket:",
		"",
		"见 [安装指南](doc:setup) 与 [[安装指南]]、[[setup|这里]]，Issue devlive-community/knowforge#12 与 #34。",
		"",
		"<CardGroup cols={2}><Card title=\"工单路由\" icon=\"headset\" href=\"/books\">",
		"按规则**分配**。",
		"</Card>",
		"</CardGroup>",
		"",
		":::tabs",
		"=== \"安装方式\"",
		"运行 `npm install` 即可。",
		":::",
		"",
		"> [!TIP]",
		"> 提示内容",
		"",
		"| 名称 | 说明 |",
		"|:---|---:|",
		"| 甲 | 乙 |",
		"",
		"!btn[开始使用](/books){bg-emerald-600} !tip[SSE](服务端推送) !switch[自动保存](on)",
		"",
		"![示意图](https://example.com/a.png =320x center)",
		"",
		":::mermaid",
		"flowchart LR",
		"  A[写作] --> B[发布]",
		":::",
		"",
		"```go",
		"// 注释",
		"fmt.Println(\"你好\")",
		"```",
		"",
		":::api GET /api/v1/books/{id}",
		"获取书籍。",
		"",
		"=== \"请求参数\"",
		"    | 参数 | 说明 |",
		"    |---|---|",
		"    | id | 书籍 ID |",
		":::",
	}, "\n")
	m := Mask(src, Options{WikiTarget: func(target string) (string, bool) {
		if target == "安装指南" {
			return "setup", true
		}
		return "", false
	}})
	visible := placeholder.ReplaceAllString(m.Text, "")
	for _, word := range []string{"children", "toc", "Card", "cols", "headset", "href", "icon", "url:", "title:", "description:", "doc:", "setup", "knowforge", "#12", "#34", ":::", "tabs", "===", "TIP", "---", "btn", "bg-emerald", "switch", "(on)", "=320x", "center", "flowchart", "写作", "fmt.Println", "npm install", "GET", "/api/v1", "rocket", "|:---|"} {
		if strings.Contains(visible, word) {
			t.Fatalf("交给翻译的文本中不应出现 %q：\n%s", word, visible)
		}
	}
	// front-matter 整块不进正文：title / description 另行翻译
	if got := m.FrontTexts(); len(got) != 2 || got[0] != "常见用例指南" || got[1] != "探索生产指南" {
		t.Fatalf("front-matter 待译文字异常: %v", got)
	}
	if strings.Contains(visible, "常见用例指南") {
		t.Fatalf("front-matter 不应留在正文中: %s", visible)
	}
	m.SetFrontTranslations([]string{"Use case guides", "Production \"guides\"\nfor you"})
	for _, text := range []string{"概览", "安装指南", "这里", "工单路由", "分配", "安装方式", "提示内容", "名称", "开始使用", "SSE", "服务端推送", "自动保存", "示意图", "请求参数", "获取书籍"} {
		if !strings.Contains(visible, text) {
			t.Fatalf("可翻译的文字 %q 不应被隐藏：\n%s", text, visible)
		}
	}
	// 模拟翻译：替换中文，占位符原样保留
	translated := strings.NewReplacer("工单路由", "Ticket routing", "安装指南", "Setup guide", "安装方式", "Install").Replace(m.Text)
	out, err := m.Restore(translated)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"---\ntitle: Use case guides\nurl: https://example.com/docs\ndescription: \"Production guides for you\"\nicon: book\n---\n<!-- icon: database -->", "<Card title=\"Ticket routing\" icon=\"headset\" href=\"/books\">", "[[setup|Setup guide]]", "=== \"Install\"", "[children]", "url: https://example.com/docs", "icon: book", "fmt.Println(\"你好\")", "  A[写作] --> B[发布]", "    |---|---|"} {
		if !strings.Contains(out, want) {
			t.Fatalf("还原结果缺少 %q：\n%s", want, out)
		}
	}
}

func TestCheckSegmentAndStream(t *testing.T) {
	m := Mask("点击 [这里](https://a.com) 查看 `code`。", Options{})
	if CheckSegment(m.Text, m.Text) != nil {
		t.Fatal("原样应通过")
	}
	dropped := regexp.MustCompile(`⟦0⟧`).ReplaceAllString(m.Text, "")
	if CheckSegment(m.Text, dropped) == nil {
		t.Fatal("丢失占位符应失败")
	}
	if CheckSegment(m.Text, m.Text+"⟦0⟧") == nil {
		t.Fatal("重复占位符应失败")
	}
	if _, err := m.Restore("⟦99⟧"); err == nil {
		t.Fatal("未知占位符应报错")
	}
	// 模型偶尔在占位符内加空格
	if out, err := m.Restore("see ⟦ 0 ⟧"); err != nil || out != "see `code`" {
		t.Fatalf("应容忍占位符内的空格: %q %v", out, err)
	}
	ready, pending := m.RestoreStream("Click [here⟦")
	if ready != "Click [here" || pending != "⟦" {
		t.Fatalf("流式还原应等待未闭合的占位符: %q %q", ready, pending)
	}
	if HasText("⟦1⟧ ⟦2⟧\n\n⟦3⟧。") || !HasText("⟦1⟧ 文字") {
		t.Fatal("HasText 判断错误")
	}
}

// 模型把整行占位符挪到行中（如在前面加了文字）时，还原后仍独占一行，结构不被破坏。
func TestRestoreKeepsLineTokensOnTheirOwnLine(t *testing.T) {
	m := Mask(":::tabs\n=== \"安装\"\n内容\n:::\n\n[children]\n\n    | a |", Options{})
	out, err := m.Restore("[EN] " + strings.ReplaceAll(m.Text, "\n\n", " "))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\n:::tabs\n", "\n=== \"安装\"", "\n:::\n", "\n[children]\n", "\n    | a |"} {
		if !strings.Contains("\n"+out+"\n", want) {
			t.Fatalf("还原后应包含 %q:\n%s", want, out)
		}
	}
}

// 行首的组件标签（块级组件）被模型挪到行中时，还原后放回行首。
func TestRestoreKeepsBlockTagsAtLineStart(t *testing.T) {
	m := Mask("<CardGroup cols={2}>\n<Card title=\"路由\" icon=\"headset\">\n正文\n</Card>\n</CardGroup>\n\n行内 <b>加粗</b> 标签", Options{})
	out, err := m.Restore("[EN] " + m.Text)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[EN] \n<CardGroup cols={2}>\n<Card title=\"路由\" icon=\"headset\">") || !strings.Contains(out, "行内 <b>加粗</b> 标签") {
		t.Fatalf("块级标签应在行首、行内标签不受影响:\n%s", out)
	}
}
