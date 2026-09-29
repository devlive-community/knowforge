package contentcollect

import (
	"regexp"
	"strings"
)

// 文档站页尾的反馈组件（「Was this page helpful?」+ 是/否按钮）常常没有可识别的 class，
// 只能按文字识别：正文末尾若干行内出现这类提问时，从该行起截掉（其后只剩按钮、上一页/下一页等页面装饰）。

const feedbackTailLines = 12 // 只在末尾这么多个非空行内查找，避免误删正文中恰好提到这句话的段落

var pageFeedbackPrompt = regexp.MustCompile(`(?i)^(?:` +
	`(?:was|is) this (?:page|article|doc|document|guide|section|content|information)? ?(?:helpful|useful)` +
	`|did this (?:page|article|doc|guide) (?:help|answer your question)(?: you)?` +
	`|how (?:helpful|useful) was this (?:page|article|doc|guide)` +
	`|(?:这|此|本)(?:篇|个)?(?:页面?|文章|文档|内容|指南)(?:内容)?(?:对[你您])?(?:有(?:所)?帮助|有用)(?:吗|么)?` +
	`|(?:这|此|本)(?:篇|个)?(?:页面?|文章|文档|内容)(?:是否)?(?:对[你您])?有(?:所)?帮助` +
	`)\s*[?？!！.。:：]*$`)

// markdownLineText 去掉行首的标题/列表/引用标记与强调符号，得到用于匹配的纯文本
var markdownLinePrefix = regexp.MustCompile(`^(?:#{1,6}\s+|[-*+]\s+|>\s*)+`)

func markdownLineText(line string) string {
	t := markdownLinePrefix.ReplaceAllString(strings.TrimSpace(line), "")
	return strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "*", "", "_", "").Replace(t))
}

// trimPageFeedback 截掉正文末尾的页面反馈提问及其后的内容。
func trimPageFeedback(markdown string) string {
	lines := strings.Split(markdown, "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < feedbackTailLines; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if pageFeedbackPrompt.MatchString(markdownLineText(lines[i])) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n")
		}
	}
	return markdown
}
