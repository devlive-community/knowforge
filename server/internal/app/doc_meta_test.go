package app

import "testing"

// 章节元数据：只认文档开头的 front-matter 与图标注释（规则与前端 lib/doc-meta.ts 一致）。
func TestSplitDocMeta(t *testing.T) {
	fm := "---\ntitle: 常见用例指南\nurl: https://platform.claude.com/docs/zh-CN/about-claude/use-case-guides/overview\ndescription: \"探索常见用例\"\n---\n"
	meta, body := splitDocMeta(fm + "\n# 正文\n内容")
	if meta["title"] != "常见用例指南" || meta["description"] != "探索常见用例" || meta["url"] == "" || body != "# 正文\n内容" {
		t.Fatalf("front-matter 解析异常: %v %q", meta, body)
	}
	meta, body = splitDocMeta("---\nicon: book\ntitle: x\n---\n<!-- icon: database -->\n正文")
	if meta["icon"] != "database" || body != "正文" {
		t.Fatalf("图标注释应优先: %v %q", meta, body)
	}
	for _, src := range []string{"正文\n---\ntitle: x\n---\n", "---\n普通段落\n---\n", "\n---\ntitle: x\n---\n", "---\ntitle: 未闭合"} {
		if meta, body := splitDocMeta(src); len(meta) != 0 || body != src {
			t.Fatalf("非开头或非法的块不是元数据: %q → %v", src, meta)
		}
	}
	cases := map[string]string{
		"<!-- icon: Database -->\n正文":               "database",
		"---\nicon: rocket\n---\n正文":                "rocket",
		"---\ntitle: x\n---\n\n<!-- icon: gear -->": "gear",
		"正文\n<!-- icon: gear -->":                   "",
		"<!-- icon: <script> -->":                   "",
	}
	for src, want := range cases {
		if got := extractDocIcon(src); got != want {
			t.Fatalf("extractDocIcon(%q) = %q, want %q", src, got, want)
		}
	}
}
