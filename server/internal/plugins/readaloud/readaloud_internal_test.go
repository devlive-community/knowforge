package readaloud

import (
	"strings"
	"testing"
)

func TestVisibleSourceMatchesRenderedText(t *testing.T) {
	md := "见 [官方文档](https://example.com/docs \"标题\") 与 <a href=\"https://x.test\">站点</a>，按钮 !btn[开始](/start){primary} 图标 :rocket{16}: 结束。\n\n[ref]: https://ref.test\n\n![图](https://img.test/a.png =100x50)"
	src := normalize(visibleSource(md))
	for _, rendered := range []string{"见 官方文档 与 站点，按钮 开始 图标 结束。", "图"} {
		if !strings.Contains(src, normalize(rendered)) {
			t.Fatalf("排版后的文字 %q 应能在源文中找到: %s", rendered, src)
		}
	}
	if strings.Contains(src, "examplecom") || strings.Contains(src, "reftest") {
		t.Fatalf("链接地址应去掉: %s", src)
	}
}
