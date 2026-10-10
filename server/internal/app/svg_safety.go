package app

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

// SVG 上传安全检查：SVG 可能被直接打开或经对象存储（没有上传目录的 sandbox CSP）分发，
// 拒绝脚本、事件属性、危险链接与实体声明。前端 lib/upload.ts 的 checkSvgText 使用相同的规则。

var (
	errUnsafeSVG  = errors.New("SVG 中包含脚本、事件属性或外部危险内容，已拒绝上传")
	errInvalidSVG = errors.New("不是有效的 SVG 文件")

	unsafeSVGTags = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true, "handler": true, "listener": true}
	unsafeSVGURL  = regexp.MustCompile(`(?i)^\s*(javascript|vbscript|data:text/html)`)
	svgEntityDecl = regexp.MustCompile(`(?i)<!ENTITY`)
	svgStyleRisk  = regexp.MustCompile(`(?i)javascript:|expression\(`)
)

// checkSVG 检查 SVG 内容；安全时返回 nil。
func checkSVG(data []byte) error {
	if svgEntityDecl.Match(data) {
		return errUnsafeSVG
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errInvalidSVG
		}
		el, isStart := tok.(xml.StartElement)
		if !isStart {
			continue
		}
		name := strings.ToLower(el.Name.Local)
		if !sawRoot {
			if name != "svg" {
				return errInvalidSVG
			}
			sawRoot = true
		}
		if unsafeSVGTags[name] {
			return errUnsafeSVG
		}
		for _, attr := range el.Attr {
			key := strings.ToLower(attr.Name.Local)
			if strings.HasPrefix(key, "on") {
				return errUnsafeSVG
			}
			if (key == "href" || key == "src") && unsafeSVGURL.MatchString(attr.Value) {
				return errUnsafeSVG
			}
			if key == "style" && svgStyleRisk.MatchString(attr.Value) {
				return errUnsafeSVG
			}
		}
	}
	if !sawRoot {
		return errInvalidSVG
	}
	return nil
}
