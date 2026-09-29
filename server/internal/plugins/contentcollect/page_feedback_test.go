package contentcollect

import "testing"

func TestTrimPageFeedback(t *testing.T) {
	cases := map[string]string{
		"正文段落\n\nWas this page helpful?":                                "正文段落",
		"正文段落\n\n**Was this page helpful?**\n\nYes\n\nNo":               "正文段落",
		"正文\n\n### Is this article useful\n\n[Previous](/a) [Next](/b)": "正文",
		"正文\n\n这篇文章对你有帮助吗？\n\n有帮助 没帮助":                                  "正文",
		"正文\n\n此页面有帮助吗":                                                 "正文",
		"正文\n\n本文档内容对您有帮助吗？":                                            "正文",
		// 正文中提到这句话（不在末尾，或不是单独一行）不删
		"Was this page helpful?\n" + "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm": "Was this page helpful?\na\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm",
		"结尾问读者：Was this page helpful? 欢迎反馈":                                  "结尾问读者：Was this page helpful? 欢迎反馈",
	}
	for in, want := range cases {
		if got := trimPageFeedback(in); got != want {
			t.Fatalf("trimPageFeedback(%q) = %q, want %q", in, got, want)
		}
	}
}
