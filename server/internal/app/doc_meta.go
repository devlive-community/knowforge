package app

import (
	"regexp"
	"strings"
)

// 章节元数据：只认文档「开头」的两种写法（可同时使用），与前端 app/web/lib/doc-meta.ts 规则一致：
//   - front-matter：第一行为 ---，到下一行 --- 结束，每行 key: value（值可加引号，缩进行为上一键的续行）；
//     有任何一行不是这种格式时整块不算元数据；
//   - 图标注释 <!-- icon: xxx -->：位于开头（或紧跟 front-matter 之后），优先于 front-matter 中的 icon。
// 正文中出现的同样内容一律按普通 Markdown 处理。

var (
	docMetaKeyLine     = regexp.MustCompile(`^([A-Za-z_][\w-]*)[ \t]*:[ \t]?(.*)$`)
	docMetaIconComment = regexp.MustCompile(`(?i)^[ \t]*<!--\s*icon:\s*([^>]*?)\s*-->[ \t]*(?:\r?\n|$)`)
	docMetaBlankLines  = regexp.MustCompile(`^(?:[ \t]*\r?\n)*`)
)

func unquoteMeta(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

// parseDocFrontMatter 解析开头的 front-matter，返回字段与所占长度；不是合法元数据块时 ok 为 false。
func parseDocFrontMatter(src string) (fields map[string]string, length int, ok bool) {
	first, rest, found := strings.Cut(src, "\n")
	if !found || strings.TrimRight(first, " \t\r") != "---" {
		return nil, 0, false
	}
	fields = map[string]string{}
	pos := len(first) + 1
	lastKey := ""
	for i := 0; i < 200; i++ {
		line, after, more := strings.Cut(rest, "\n")
		next := pos + len(line) + 1
		clean := strings.TrimRight(line, "\r")
		if t := strings.TrimRight(clean, " \t"); t == "---" || t == "..." {
			if len(fields) == 0 {
				return nil, 0, false
			}
			if !more {
				next = len(src)
			}
			return fields, next, true
		}
		if trimmed := strings.TrimSpace(clean); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if m := docMetaKeyLine.FindStringSubmatch(clean); m != nil {
				lastKey = m[1]
				fields[lastKey] = unquoteMeta(m[2])
			} else if (clean[0] == ' ' || clean[0] == '\t') && lastKey != "" {
				part := unquoteMeta(strings.TrimPrefix(trimmed, "- "))
				fields[lastKey] = strings.TrimSpace(fields[lastKey] + " " + part)
			} else {
				return nil, 0, false
			}
		}
		if !more {
			return nil, 0, false
		}
		rest, pos = after, next
	}
	return nil, 0, false
}

// splitDocMeta 拆分开头的元数据与正文。
func splitDocMeta(content string) (map[string]string, string) {
	src := strings.TrimPrefix(content, "\ufeff")
	meta := map[string]string{}
	offset := 0
	if fields, n, ok := parseDocFrontMatter(src); ok {
		meta, offset = fields, n
	}
	// 图标注释只认一条（开头的第一条），之后的同样注释属于正文
	rest := src[offset:]
	lead := docMetaBlankLines.FindString(rest)
	if m := docMetaIconComment.FindStringSubmatch(rest[len(lead):]); m != nil {
		if icon := strings.TrimSpace(m[1]); icon != "" {
			meta["icon"] = icon
		}
		offset += len(lead) + len(m[0])
	}
	if offset == 0 {
		return meta, src
	}
	return meta, docMetaBlankLines.ReplaceAllString(src[offset:], "")
}

// docBody 去掉开头元数据后的正文（导出等只需要正文的场景）。
func docBody(content string) string {
	_, body := splitDocMeta(content)
	return body
}
