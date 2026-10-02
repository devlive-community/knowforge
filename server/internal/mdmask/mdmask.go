// Package mdmask 在把 Markdown 交给翻译（AI 或机器翻译）之前，把不能翻译的语法替换为占位符 ⟦n⟧，
// 翻译后再原样还原，保证 [children]、[toc]、组件标签、::: 块、链接地址、图标、front-matter 等
// 不会被翻译或改写。只把读者能看到的文字留给翻译：段落、标题、列表文字、链接文字、图片说明、
// 组件的 title 属性、标签页标题、!btn / !tip 的文字、front-matter 的 title 与 description。
//
// 用法：m := mdmask.Mask(src, opts)；把 m.Text 交给翻译；out, err := m.Restore(translated)。
// 翻译结果缺少或多出占位符时 Restore 返回 ErrPlaceholders（调用方可重试）。
package mdmask

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ErrPlaceholders 译文中的占位符与原文不一致（被删改、重复或新增）。
var ErrPlaceholders = errors.New("译文未能完整保留原文的 Markdown 语法")

// PromptRule 给翻译模型的要求（加入系统提示）。
const PromptRule = "文中形如 ⟦数字⟧ 的占位符代表不可翻译的格式或代码，必须原样保留：不得翻译、修改、删除、增加或合并，保持其在句中的相对位置"

// Options 可选的掩码行为。
type Options struct {
	// WikiTarget 把双向链接 [[目标]] 的目标解析为章节 slug（如按标题查找）；返回 ok 时改写为 [[slug|目标]]，
	// 目标文字作为显示文字参与翻译，链接仍指向同一章节。为 nil 或未解析时整个 [[目标]] 保持原样。
	WikiTarget func(target string) (slug string, ok bool)
}

// Masked 掩码结果。
type Masked struct {
	Text   string   // 交给翻译的文本
	tokens []string // 占位符 ⟦i⟧ 对应的原文
	kinds  []int    // 占位符的位置约束
	front  *frontMatter
}

// frontMatter 文档开头的 front-matter：整块作为一个占位符（不交给正文翻译，避免模型在块前插入文字而破坏结构），
// 其中 title / description 的值另行翻译（FrontTexts / SetFrontTranslations）。
type frontMatter struct {
	token  int
	lines  []string
	values []frontValue
}

type frontValue struct {
	line           int
	prefix, suffix string
	text           string
}

// 占位符的位置约束：行内（随文字移动）、行首前缀（=== " 与缩进，须在行首）、整行（::: 行、宏、代码块等，须独占一行）。
const (
	kindInline = iota
	kindLinePrefix
	kindLine
)

type masker struct {
	tokens []string
	kinds  []int
	opts   Options
}

func (m *masker) keepKind(s string, kind int) string {
	if s == "" {
		return ""
	}
	m.tokens = append(m.tokens, s)
	m.kinds = append(m.kinds, kind)
	return "⟦" + strconv.Itoa(len(m.tokens)-1) + "⟧"
}

func (m *masker) keep(s string) string       { return m.keepKind(s, kindInline) }
func (m *masker) keepLine(s string) string   { return m.keepKind(s, kindLine) }
func (m *masker) keepPrefix(s string) string { return m.keepKind(s, kindLinePrefix) }

var (
	fenceOpen     = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
	verbatimBlock = regexp.MustCompile(`^\s*:::\s*(katex|mermaid|diff)\b`)
	colonLine     = regexp.MustCompile(`^\s*:::`)
	tabTitleLine  = regexp.MustCompile(`^(\s*===\s*")([^"]*)("\s*)$`)
	macroLine     = regexp.MustCompile(`(?i)^\s*\[(toc|children)\]\s*$`)
	frontKey      = regexp.MustCompile(`^([A-Za-z_][\w-]*)([ \t]*:[ \t]?)(.*)$`)
	leadingSpace  = regexp.MustCompile(`^[ \t]{2,}`)
	openTagLine   = regexp.MustCompile(`^\s*<[A-Za-z][\w.-]*(\s[^<>]*)?$`)
)

// Mask 掩码整篇 Markdown（front-matter、代码块等块级结构需要整篇上下文，应对整篇调用后再分段翻译）。
func Mask(src string, opts Options) *Masked {
	m := &masker{opts: opts}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	i := 0

	// front-matter：仅第一行为 --- 时
	var front *frontMatter
	if len(lines) > 0 && strings.TrimSpace(strings.TrimPrefix(lines[0], "\ufeff")) == "---" {
		if end := frontMatterEnd(lines); end > 0 {
			front = &frontMatter{lines: append([]string(nil), lines[:end+1]...)}
			for j := 1; j < end; j++ {
				if v, ok := frontMatterValue(lines[j]); ok {
					v.line = j
					front.values = append(front.values, v)
				}
			}
			out = append(out, m.keepLine(strings.Join(front.lines, "\n")))
			front.token = len(m.tokens) - 1
			i = end + 1
		}
	}

	var htmlComment []string // 跨行的 HTML 注释
	for ; i < len(lines); i++ {
		line := lines[i]
		if htmlComment != nil {
			htmlComment = append(htmlComment, line)
			if strings.Contains(line, "-->") {
				out = append(out, m.keepLine(strings.Join(htmlComment, "\n")))
				htmlComment = nil
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		// 围栏代码块：整块保持原样
		if f := fenceOpen.FindStringSubmatch(line); f != nil {
			fence := f[1]
			block := []string{line}
			for i+1 < len(lines) {
				i++
				block = append(block, lines[i])
				if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, fence[:3]) && strings.Trim(t, string(fence[0])) == "" && len(t) >= len(fence) {
					break
				}
			}
			out = append(out, m.keepLine(strings.Join(block, "\n")))
			continue
		}
		// :::katex / :::mermaid / :::diff：整块保持原样（公式、图表、代码差异）
		if verbatimBlock.MatchString(line) {
			block := []string{line}
			for i+1 < len(lines) {
				i++
				block = append(block, lines[i])
				if strings.TrimSpace(lines[i]) == ":::" {
					break
				}
			}
			out = append(out, m.keepLine(strings.Join(block, "\n")))
			continue
		}
		// 跨行的 HTML 标签（属性分多行写，如 <img\n  src="…"\n  alt="…">）：到 > 为止整体保持原样
		if openTagLine.MatchString(line) {
			block := []string{line}
			j := i
			for j+1 < len(lines) && j-i < 50 {
				j++
				block = append(block, lines[j])
				if strings.Contains(lines[j], ">") {
					break
				}
			}
			if strings.Contains(block[len(block)-1], ">") {
				out = append(out, m.keepLine(strings.Join(block, "\n")))
				i = j
				continue
			}
		}
		switch {
		case trimmed == "":
			out = append(out, line)
		case strings.HasPrefix(trimmed, "<!--"):
			if strings.Contains(trimmed, "-->") {
				out = append(out, m.keepLine(line))
			} else {
				htmlComment = []string{line}
			}
		case colonLine.MatchString(line), macroLine.MatchString(line), isTableAlignLine(line):
			out = append(out, m.keepLine(line))
		default:
			if t := tabTitleLine.FindStringSubmatch(line); t != nil {
				out = append(out, m.keepPrefix(t[1])+m.inline(t[2])+m.keep(t[3]))
				continue
			}
			mark := len(m.tokens)
			lead := leadingSpace.FindString(line)
			rest, first := line[len(lead):], ""
			if loc := htmlTag.FindStringIndex(rest); loc != nil && loc[0] == 0 {
				// 行首标签与其前的缩进合为一个行首占位符
				first, rest, lead = m.tag(lead, rest[:loc[1]], true), rest[loc[1]:], ""
			}
			masked := m.keepPrefix(lead) + first + m.inline(rest)
			if !HasText(masked) {
				// 整行没有文字（如 HTML 表格的 <tr>、</td><td> 行）：撤销逐个占位，整行作为一个占位符
				m.tokens, m.kinds = m.tokens[:mark], m.kinds[:mark]
				masked = m.keepLine(line)
			}
			out = append(out, m.mergeAdjacent(masked))
		}
	}
	if htmlComment != nil {
		out = append(out, m.keepLine(strings.Join(htmlComment, "\n")))
	}
	frontToken := -1
	if front != nil {
		frontToken = front.token
	}
	return &Masked{Text: strings.Join(m.mergeLines(out, frontToken), "\n"), tokens: m.tokens, kinds: m.kinds, front: front}
}

// isTableAlignLine 表格的对齐行（|---|:---:|）：含竖线，且只由竖线、冒号、连字符与空白组成。
func isTableAlignLine(line string) bool {
	t := strings.TrimSpace(line)
	return strings.Contains(t, "|") && strings.Contains(t, "-") && strings.Trim(t, "|:- \t") == ""
}

// frontMatterEnd 返回 front-matter 结束行（---/...）的下标；每行须为 key: value 或其缩进续行，否则返回 -1。
func frontMatterEnd(lines []string) int {
	for j := 1; j < len(lines) && j < 200; j++ {
		t := strings.TrimRight(lines[j], " \t")
		if t == "---" || t == "..." {
			if j == 1 {
				return -1
			}
			return j
		}
		if strings.TrimSpace(t) == "" || strings.HasPrefix(strings.TrimSpace(t), "#") || t[0] == ' ' || t[0] == '\t' {
			continue
		}
		if !frontKey.MatchString(t) {
			return -1
		}
	}
	return -1
}

// frontMatterValue 需要翻译的 front-matter 行（title / description）：拆出值与前后缀（键、引号）。
func frontMatterValue(line string) (frontValue, bool) {
	kv := frontKey.FindStringSubmatch(line)
	if kv == nil {
		return frontValue{}, false
	}
	if key := strings.ToLower(kv[1]); key != "title" && key != "description" {
		return frontValue{}, false
	}
	value := kv[3]
	v := frontValue{prefix: kv[1] + kv[2]}
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		v.prefix += value[:1]
		v.suffix = value[len(value)-1:]
		value = value[1 : len(value)-1]
	}
	v.text = value
	return v, hasText(value)
}

// FrontTexts front-matter 中需要翻译的文字（title、description 的值），没有时为空。
func (m *Masked) FrontTexts() []string {
	if m.front == nil {
		return nil
	}
	out := make([]string, len(m.front.values))
	for i, v := range m.front.values {
		out[i] = v.text
	}
	return out
}

// SetFrontTranslations 用译文替换 front-matter 中对应的值（与 FrontTexts 等长、同序；不调用时保持原文）。
// 译文中的换行与同类引号会被去掉，保证 front-matter 仍是一行一个键。
func (m *Masked) SetFrontTranslations(texts []string) {
	if m.front == nil || len(texts) != len(m.front.values) {
		return
	}
	lines := append([]string(nil), m.front.lines...)
	for i, v := range m.front.values {
		t := strings.Join(strings.Fields(strings.ReplaceAll(texts[i], "\n", " ")), " ")
		if v.suffix != "" {
			t = strings.ReplaceAll(t, v.suffix, "")
		}
		if t == "" {
			continue
		}
		lines[v.line] = v.prefix + t + v.suffix
	}
	m.tokens[m.front.token] = strings.Join(lines, "\n")
}

var (
	inlineCode   = regexp.MustCompile("(`+)[^`]*?(`+)")
	tipSyntax    = regexp.MustCompile(`!tip\[([^\]]*)\]\(([^)]*)\)`)
	btnSyntax    = regexp.MustCompile(`!(btn|switch)\[([^\]]*)\](\([^)]*\))?(\{[^}]*\})?`)
	wikiLink     = regexp.MustCompile(`\[\[([^\[\]\n|]+)(\|[^\[\]\n]+)?\]\]`)
	htmlTag      = regexp.MustCompile(`</?[A-Za-z][\w.-]*(?:\s+[^<>]*?)?\s*/?>`)
	tagTitleAttr = regexp.MustCompile(`(\stitle\s*=\s*)(?:"([^"]*)"|'([^']*)'|\{"([^"]*)"\})`)
	autoLink     = regexp.MustCompile(`<(?:https?://|mailto:)[^<>\s]+>`)
	linkDest     = regexp.MustCompile(`\]\((?:[^()\s]|\([^()\s]*\))*(?:\s+"[^"]*")?(?:\s*=\d*x\d*)?(?:\s+(?:left|center|right))?\s*\)`)
	alertMarker  = regexp.MustCompile(`(?i)\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]`)
	inlineMacro  = regexp.MustCompile(`(?i)\[(toc|children)\]`)
	iconSyntax   = regexp.MustCompile(`:[a-zA-Z][a-zA-Z-]+(?:\{[^}]+\})?:`)
	issueRef     = regexp.MustCompile(`(?:\b[a-zA-Z0-9-]+/[a-zA-Z0-9_.-]+)?#\d+\b`)
	bareURL      = regexp.MustCompile(`https?://[^\s<>()\[\]⟦⟧]+`)
	placeholder  = regexp.MustCompile(`⟦\s*(\d+)\s*⟧`)
)

// inline 掩码一行（或一段）文字中的行内语法。
func (m *masker) inline(s string) string {
	s = inlineCode.ReplaceAllStringFunc(s, func(v string) string {
		sub := inlineCode.FindStringSubmatch(v)
		if len(sub[1]) != len(sub[2]) {
			return v
		}
		return m.keep(v)
	})
	s = tipSyntax.ReplaceAllStringFunc(s, func(v string) string {
		sub := tipSyntax.FindStringSubmatch(v)
		return m.keep("!tip[") + sub[1] + m.keep("](") + sub[2] + m.keep(")")
	})
	s = btnSyntax.ReplaceAllStringFunc(s, func(v string) string {
		sub := btnSyntax.FindStringSubmatch(v)
		return m.keep("!"+sub[1]+"[") + sub[2] + m.keep("]"+sub[3]+sub[4])
	})
	s = wikiLink.ReplaceAllStringFunc(s, func(v string) string {
		sub := wikiLink.FindStringSubmatch(v)
		target, label := strings.TrimSpace(sub[1]), strings.TrimPrefix(sub[2], "|")
		if label != "" {
			return m.keep("[["+sub[1]+"|") + label + m.keep("]]")
		}
		if m.opts.WikiTarget != nil && !strings.Contains(target, "/") {
			if slug, ok := m.opts.WikiTarget(target); ok {
				return m.keep("[["+slug+"|") + target + m.keep("]]")
			}
		}
		return m.keep(v)
	})
	s = autoLink.ReplaceAllStringFunc(s, m.keep)
	s = htmlTag.ReplaceAllStringFunc(s, func(v string) string { return m.tag("", v, false) })
	s = linkDest.ReplaceAllStringFunc(s, m.keep)
	s = alertMarker.ReplaceAllStringFunc(s, m.keep)
	s = inlineMacro.ReplaceAllStringFunc(s, m.keep)
	s = iconSyntax.ReplaceAllStringFunc(s, m.keep)
	s = issueRef.ReplaceAllStringFunc(s, m.keep)
	s = bareURL.ReplaceAllStringFunc(s, m.keep)
	return s
}

var adjacentPlaceholders = regexp.MustCompile(`⟦(\d+)⟧([ \t]*)⟦(\d+)⟧`)

// mergeAdjacent 把一行内只隔着空白的相邻占位符合并为一个（如 HTML 表格中连续的标签），
// 占位符越少，模型越不容易漏掉或改动。行首前缀只与紧随的行内占位符合并，合并后仍按行首前缀处理。
func (m *masker) mergeAdjacent(line string) string {
	for {
		loc := adjacentPlaceholders.FindStringSubmatchIndex(line)
		if loc == nil {
			return line
		}
		a, _ := strconv.Atoi(line[loc[2]:loc[3]])
		b, _ := strconv.Atoi(line[loc[6]:loc[7]])
		if m.kinds[b] != kindInline || m.kinds[a] == kindLine {
			// 不能合并（后者须在行首或独占一行）：跳过这一对，继续处理其后的内容
			return line[:loc[5]] + m.mergeAdjacent(line[loc[5]:])
		}
		m.tokens[a] += line[loc[4]:loc[5]] + m.tokens[b]
		m.tokens[b] = ""
		line = line[:loc[0]] + "⟦" + strconv.Itoa(a) + "⟧" + line[loc[1]:]
	}
}

var singleLinePlaceholder = regexp.MustCompile(`^⟦(\d+)⟧$`)

// mergeLines 把相邻的整行占位符（中间没有空行）合并为一个，如整段 HTML 表格结构只剩少数几个占位符。
// keep 为不参与合并的占位符（front-matter，其内容会单独替换为译文）。
func (m *masker) mergeLines(lines []string, keep int) []string {
	out := make([]string, 0, len(lines))
	prev := -1 // 上一行若为单独的整行占位符，记录其编号
	for _, line := range lines {
		if sub := singleLinePlaceholder.FindStringSubmatch(line); sub != nil {
			n, _ := strconv.Atoi(sub[1])
			if m.kinds[n] == kindLine && n != keep {
				if prev >= 0 {
					m.tokens[prev] += "\n" + m.tokens[n]
					m.tokens[n] = ""
					continue
				}
				prev = n
				out = append(out, line)
				continue
			}
		}
		prev = -1
		out = append(out, line)
	}
	return out
}

// tag 掩码一个 HTML/组件标签：title 属性的值参与翻译，其余保持原样。lineStart 为真时标签位于行首
// （<Tabs>、<Card …> 等块级组件），其前缀占位符须留在行首。
func (m *masker) tag(lead, v string, lineStart bool) string {
	head := func(s string) string { return m.keep(lead + s) }
	if lineStart {
		head = func(s string) string { return m.keepPrefix(lead + s) }
	}
	loc := tagTitleAttr.FindStringSubmatchIndex(v)
	if loc != nil {
		for g := 2; g <= 4; g++ {
			if start, end := loc[2*g], loc[2*g+1]; start >= 0 {
				if hasText(v[start:end]) {
					return head(v[:start]) + v[start:end] + m.keep(v[end:])
				}
				break
			}
		}
	}
	return head(v)
}

func hasText(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// HasText 掩码后的片段是否还有需要翻译的文字（只剩占位符、空白与标点时不必交给翻译）。
func HasText(masked string) bool {
	return hasText(placeholder.ReplaceAllString(masked, ""))
}

// Placeholders 片段中出现的占位符编号（按出现顺序）。
func Placeholders(s string) []int {
	var ids []int
	for _, sub := range placeholder.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(sub[1])
		ids = append(ids, n)
	}
	return ids
}

// CheckSegment 校验某一片段的译文：占位符的集合必须与原片段一致（每个恰好出现一次）。
func CheckSegment(original, translated string) error {
	want := map[int]int{}
	for _, n := range Placeholders(original) {
		want[n]++
	}
	got := map[int]int{}
	for _, n := range Placeholders(translated) {
		got[n]++
	}
	if len(want) != len(got) {
		return ErrPlaceholders
	}
	for n, c := range want {
		if got[n] != c {
			return ErrPlaceholders
		}
	}
	return nil
}

// Restore 把译文（整篇或其中一段）中的占位符还原为原文；出现未知编号时返回 ErrPlaceholders。
// 整行占位符（::: 行、宏、代码块等）若被模型挪到了行中，会补上换行使其独占一行；行首前缀同理放回行首。
func (m *Masked) Restore(translated string) (string, error) {
	var b strings.Builder
	bad := false
	last := 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(translated, -1) {
		b.WriteString(translated[last:loc[0]])
		last = loc[1]
		n, err := strconv.Atoi(translated[loc[2]:loc[3]])
		if err != nil || n < 0 || n >= len(m.tokens) {
			bad = true
			b.WriteString(translated[loc[0]:loc[1]])
			continue
		}
		kind := kindInline
		if n < len(m.kinds) {
			kind = m.kinds[n]
		}
		if kind != kindInline {
			// 须在行首的占位符前面若有文字（模型把它挪到了行中），补换行放回行首
			if cur := b.String(); cur != "" && !strings.HasSuffix(cur, "\n") {
				if lineStart := strings.LastIndex(cur, "\n") + 1; strings.TrimSpace(cur[lineStart:]) != "" {
					b.WriteString("\n")
				}
			}
		}
		b.WriteString(m.tokens[n])
		if kind == kindLine && last < len(translated) && translated[last] != '\n' {
			rest := translated[last:]
			if nl := strings.IndexByte(rest, '\n'); strings.TrimSpace(rest[:max(nl, 0)]) != "" || (nl < 0 && strings.TrimSpace(rest) != "") {
				b.WriteString("\n")
			}
			// 去掉整行占位符后面同一行多余的空白
			for last < len(translated) && (translated[last] == ' ' || translated[last] == '\t') {
				last++
			}
		}
	}
	b.WriteString(translated[last:])
	if bad {
		return b.String(), ErrPlaceholders
	}
	return b.String(), nil
}

// RestoreStream 还原流式片段：返回可以安全输出的部分（已还原）与需要等待后续文字的尾部（未闭合的占位符）。
func (m *Masked) RestoreStream(buffered string) (ready, pending string) {
	cut := len(buffered)
	if open := strings.LastIndex(buffered, "⟦"); open >= 0 && !strings.Contains(buffered[open:], "⟧") {
		cut = open
	}
	ready, _ = m.Restore(buffered[:cut])
	return ready, buffered[cut:]
}
