package paidcontent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 试读：不超过比例（整章一段、首段很长时也一样），尽量停在句末，代码块补齐结束标记。
func TestMakePreview(t *testing.T) {
	single := strings.Repeat("这是一整段没有空行的正文。", 50) // 650 字，只有一段
	p := makePreview(single, 30)
	if n := utf8.RuneCountInString(p); n > 650*30/100+1 || n < 100 || !strings.HasSuffix(p, "。…") {
		t.Fatalf("整章一段时应按比例截断并停在句末: %d %q", n, p)
	}

	paras := strings.Repeat("甲", 50) + "\n\n" + strings.Repeat("乙", 50) + "\n\n" + strings.Repeat("丙", 100) // 204 字
	p = makePreview(paras, 50)                                                                              // 102 字
	if !strings.HasPrefix(p, strings.Repeat("甲", 50)+"\n\n"+strings.Repeat("乙", 50)) || strings.Contains(p, "丙") {
		t.Fatalf("放得下的段落应完整保留，超出部分不给: %q", p)
	}

	long := strings.Repeat("长", 900) + "\n\n" + strings.Repeat("短", 100) // 首段占 90%
	p = makePreview(long, 30)
	if n := utf8.RuneCountInString(p); n > 1002*30/100+1 || strings.Contains(p, "短") {
		t.Fatalf("首段很长时也不应超过比例: %d", n)
	}

	code := "说明\n\n```go\n" + strings.Repeat("fmt.Println(1)\n", 40) + "```\n\n结尾秘密"
	p = makePreview(code, 30)
	if strings.Count(p, "```")%2 != 0 || strings.Contains(p, "结尾秘密") {
		t.Fatalf("代码块中截断应补齐结束标记: %q", p)
	}

	if makePreview(single, 0) != "" || makePreview("", 30) != "" {
		t.Fatal("比例为 0 或正文为空时没有试读")
	}
}
