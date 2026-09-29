package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // 注册 GIF 解码器（读取图片尺寸）
	_ "image/jpeg" // 注册 JPEG 解码器
	_ "image/png"  // 注册 PNG 解码器
	"net/http"
	"strings"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const (
	docxEMUPerPixel = 9525    // 96dpi 下每像素的 EMU
	docxMaxImageEMU = 5486400 // 约 6 英寸正文宽度上限
)

// ExportBookDOCX GET /books/:id/export/docx 导出书籍为 Word 文档（纯 Go 生成 OOXML，无需插件）。
func (a *App) ExportBookDOCX(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canExportBook(u, book) {
		fail(c, http.StatusForbidden, "该书籍未开放导出")
		return
	}
	if !formatAllowed(book, "docx") {
		fail(c, http.StatusForbidden, "作者未开放 Word 导出")
		return
	}
	a.recordBookExport(u, book, "docx")
	a.writeBookDOCX(c, book, a.canEditBookContent(u, book))
}

type docxRunStyle struct {
	bold, italic, strike, code bool
}

// docxRenderer 将 goldmark AST 渲染为 WordprocessingML，收集需内嵌的本地图片。
type docxRenderer struct {
	src        []byte
	body       *strings.Builder
	images     map[string][]byte // 图片名 -> 数据（本站已上传图片）
	imageRels  map[string]string // 图片名 -> 关系 ID
	imageOrder []string          // 稳定顺序，用于写 media 与 rels
	relSeq     int               // 关系 ID 计数（rId1 保留给 styles.xml）
}

func docxEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func docxRun(s string, st docxRunStyle) string {
	if s == "" {
		return ""
	}
	props := ""
	if st.bold {
		props += "<w:b/>"
	}
	if st.italic {
		props += "<w:i/>"
	}
	if st.strike {
		props += "<w:strike/>"
	}
	if st.code {
		props += `<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/>`
	}
	rpr := ""
	if props != "" {
		rpr = "<w:rPr>" + props + "</w:rPr>"
	}
	return "<w:r>" + rpr + `<w:t xml:space="preserve">` + docxEscape(s) + "</w:t></w:r>"
}

func docxRunGray(s string) string {
	return `<w:r><w:rPr><w:color w:val="9CA3AF"/><w:sz w:val="18"/></w:rPr><w:t xml:space="preserve">` + docxEscape(s) + "</w:t></w:r>"
}

// nodeText 收集节点内全部文本（Text/String 段），用于代码内联与图片替代文本。
func (r *docxRenderer) nodeText(n ast.Node) string {
	var b strings.Builder
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(r.src))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func (r *docxRenderer) inlineChildren(n ast.Node, st docxRunStyle) string {
	var b strings.Builder
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		b.WriteString(r.inline(ch, st))
	}
	return b.String()
}

func (r *docxRenderer) inline(n ast.Node, st docxRunStyle) string {
	switch t := n.(type) {
	case *ast.Text:
		out := docxRun(string(t.Segment.Value(r.src)), st)
		if t.HardLineBreak() {
			out += "<w:r><w:br/></w:r>"
		} else if t.SoftLineBreak() {
			out += docxRun(" ", st)
		}
		return out
	case *ast.String:
		return docxRun(string(t.Value), st)
	case *ast.CodeSpan:
		cs := st
		cs.code = true
		return docxRun(r.nodeText(t), cs)
	case *ast.Emphasis:
		es := st
		if t.Level >= 2 {
			es.bold = true
		} else {
			es.italic = true
		}
		return r.inlineChildren(t, es)
	case *east.Strikethrough:
		ss := st
		ss.strike = true
		return r.inlineChildren(t, ss)
	case *ast.Link:
		inner := r.inlineChildren(t, st)
		dest := strings.TrimSpace(string(t.Destination))
		if dest != "" && !strings.HasPrefix(dest, "#") {
			inner += docxRun(" ("+dest+")", docxRunStyle{italic: true})
		}
		return inner
	case *ast.AutoLink:
		return docxRun(string(t.URL(r.src)), st)
	case *ast.Image:
		return r.imageInline(t)
	case *ast.RawHTML:
		return "" // 跳过内联裸 HTML
	default:
		return r.inlineChildren(n, st)
	}
}

// imageInline 渲染内联图片：可解码的本站栅格图内嵌为 drawing，否则退化为替代文本。
func (r *docxRenderer) imageInline(img *ast.Image) string {
	alt := r.nodeText(img)
	fallback := "[图片]"
	if alt != "" {
		fallback = "[图片: " + alt + "]"
	}
	name := strings.TrimPrefix(string(img.Destination), "images/")
	data, ok := r.images[name]
	if !ok || strings.ContainsAny(name, "/\\") {
		return docxRun(fallback, docxRunStyle{italic: true})
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return docxRun(fallback, docxRunStyle{italic: true})
	}
	rid, exists := r.imageRels[name]
	if !exists {
		r.relSeq++
		rid = fmt.Sprintf("rId%d", r.relSeq)
		r.imageRels[name] = rid
		r.imageOrder = append(r.imageOrder, name)
	}
	cx := int64(cfg.Width) * docxEMUPerPixel
	cy := int64(cfg.Height) * docxEMUPerPixel
	if cx > docxMaxImageEMU {
		cy = cy * docxMaxImageEMU / cx
		cx = docxMaxImageEMU
	}
	id := len(r.imageOrder)
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Image%d"/><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:nvPicPr><pic:cNvPr id="%d" name="Image%d"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		cx, cy, id, id, id, id, rid, cx, cy)
}

func (r *docxRenderer) para(style, inner string) {
	ppr := ""
	if style != "" {
		ppr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	r.body.WriteString("<w:p>" + ppr + inner + "</w:p>")
}

func (r *docxRenderer) renderBlocks(parent ast.Node) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		r.renderBlock(n)
	}
}

func (r *docxRenderer) renderBlock(n ast.Node) {
	switch t := n.(type) {
	case *ast.Heading:
		level := t.Level
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		r.para(fmt.Sprintf("Heading%d", level), r.inlineChildren(t, docxRunStyle{}))
	case *ast.Paragraph:
		if inner := r.inlineChildren(t, docxRunStyle{}); strings.TrimSpace(inner) != "" {
			r.para("", inner)
		}
	case *ast.TextBlock:
		if inner := r.inlineChildren(t, docxRunStyle{}); strings.TrimSpace(inner) != "" {
			r.para("", inner)
		}
	case *ast.FencedCodeBlock:
		r.codeBlock(t.Lines())
	case *ast.CodeBlock:
		r.codeBlock(t.Lines())
	case *ast.Blockquote:
		r.renderQuote(t)
	case *ast.List:
		r.renderList(t, 0)
	case *ast.ThematicBreak:
		r.body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="CBD5E1"/></w:pBdr></w:pPr></w:p>`)
	case *east.Table:
		r.renderTable(t)
	case *ast.HTMLBlock:
		if txt := r.linesText(t.Lines()); strings.TrimSpace(txt) != "" {
			r.para("", docxRun(txt, docxRunStyle{}))
		}
	default:
		r.renderBlocks(n)
	}
}

func (r *docxRenderer) linesText(segs *text.Segments) string {
	var b strings.Builder
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		b.Write(seg.Value(r.src))
	}
	return b.String()
}

func (r *docxRenderer) codeBlock(segs *text.Segments) {
	var runs strings.Builder
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		line := strings.TrimRight(string(seg.Value(r.src)), "\r\n")
		if i > 0 {
			runs.WriteString("<w:r><w:br/></w:r>")
		}
		runs.WriteString(docxRun(line, docxRunStyle{}))
	}
	r.para("Code", runs.String())
}

func (r *docxRenderer) renderQuote(q ast.Node) {
	for ch := q.FirstChild(); ch != nil; ch = ch.NextSibling() {
		switch ch.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			r.para("Quote", r.inlineChildren(ch, docxRunStyle{}))
		default:
			r.renderBlock(ch)
		}
	}
}

func (r *docxRenderer) renderList(list *ast.List, depth int) {
	ordered := list.IsOrdered()
	num := list.Start
	if num == 0 {
		num = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if ordered {
			marker = fmt.Sprintf("%d. ", num)
		}
		firstDone := false
		for ch := item.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if sub, ok := ch.(*ast.List); ok {
				r.renderList(sub, depth+1)
				continue
			}
			prefix := ""
			if !firstDone {
				prefix = marker
				firstDone = true
			}
			r.listPara(depth, prefix, r.inlineChildren(ch, docxRunStyle{}))
		}
		if !firstDone {
			r.listPara(depth, marker, "")
		}
		num++
	}
}

func (r *docxRenderer) listPara(depth int, marker, inner string) {
	indent := (depth + 1) * 360
	prefix := ""
	if marker != "" {
		prefix = docxRun(marker, docxRunStyle{})
	}
	r.body.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:ind w:left="%d"/></w:pPr>%s%s</w:p>`, indent, prefix, inner))
}

func (r *docxRenderer) renderTable(tbl *east.Table) {
	cols := len(tbl.Alignments)
	if cols == 0 {
		if first := tbl.FirstChild(); first != nil {
			for ch := first.FirstChild(); ch != nil; ch = ch.NextSibling() {
				cols++
			}
		}
	}
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		b.WriteString(fmt.Sprintf(`<w:%s w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>`, side))
	}
	b.WriteString(`</w:tblBorders></w:tblPr>`)
	if cols > 0 {
		b.WriteString("<w:tblGrid>")
		for i := 0; i < cols; i++ {
			b.WriteString("<w:gridCol/>")
		}
		b.WriteString("</w:tblGrid>")
	}
	for row := tbl.FirstChild(); row != nil; row = row.NextSibling() {
		_, header := row.(*east.TableHeader)
		b.WriteString("<w:tr>")
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			st := docxRunStyle{bold: header}
			inner := r.inlineChildren(cell, st)
			if inner == "" {
				inner = docxRun(" ", st)
			}
			b.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p>` + inner + "</w:p></w:tc>")
		}
		b.WriteString("</w:tr>")
	}
	b.WriteString("</w:tbl><w:p/>") // 表格后需跟一个段落
	r.body.WriteString(b.String())
}

// writeBookDOCX 打包书籍为 .docx 并写入响应（鉴权由调用方负责）。
func (a *App) writeBookDOCX(c *gin.Context, book *models.Book, includeDrafts bool) {
	setting := a.resolveExportStyle(c.Query("style"), book, currentUser(c))
	footer := a.resolveExportFooter(book, currentUser(c))

	docs := []models.Document{}
	if err := a.DB.Where("book_id = ?", book.ID).Order("sort_order ASC, id ASC").Find(&docs).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询章节失败")
		return
	}

	r := &docxRenderer{
		body:      &strings.Builder{},
		images:    map[string][]byte{},
		imageRels: map[string]string{},
		relSeq:    1, // rId1 = styles.xml
	}
	parser := epubMarkdown.Parser()

	if setting.IncludeCover {
		r.para("Title", docxRun(book.Title, docxRunStyle{}))
		if book.User != nil && book.User.Username != "" {
			r.body.WriteString(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr>` + docxRun(book.User.Username, docxRunStyle{italic: true}) + "</w:p>")
		}
		if strings.TrimSpace(book.Description) != "" {
			r.para("", docxRun(book.Description, docxRunStyle{}))
		}
	}

	if setting.IncludeToc {
		r.para("Heading1", docxRun("目录", docxRunStyle{}))
		for _, doc := range docs {
			if !includeDrafts && doc.Status != "published" {
				continue
			}
			r.listPara(0, "• ", docxRun(doc.Title, docxRunStyle{}))
		}
	}

	for _, doc := range docs {
		if !includeDrafts && doc.Status != "published" {
			continue
		}
		content, docImages := a.rewriteUploadsToLocal(docBody(doc.Content)) // 开头的章节元数据不导出
		for n, d := range docImages {
			r.images[n] = d
		}
		r.para("Heading1", docxRun(doc.Title, docxRunStyle{}))
		r.src = []byte(content)
		r.renderBlocks(parser.Parse(text.NewReader(r.src)))
		if a.pluginEnabled(pluginWatermark) && book.WatermarkEnabled && strings.TrimSpace(book.WatermarkText) != "" {
			r.body.WriteString(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr>` + docxRunGray(book.WatermarkText) + "</w:p>")
		}
	}

	if strings.TrimSpace(footer) != "" {
		r.body.WriteString(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr>` + docxRunGray(footer) + "</w:p>")
	}

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	writeFile := func(name, content string) bool {
		fw, err := w.Create(name)
		if err == nil {
			_, err = fw.Write([]byte(content))
		}
		if err != nil {
			fail(c, http.StatusInternalServerError, "打包失败: "+err.Error())
			return false
		}
		return true
	}

	// 关系：styles.xml + 各内嵌图片
	rels := &strings.Builder{}
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
	for _, name := range r.imageOrder {
		rels.WriteString(fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/%s"/>`, r.imageRels[name], docxEscape(name)))
	}
	rels.WriteString(`</Relationships>`)

	if !writeFile("[Content_Types].xml", docxContentTypes) ||
		!writeFile("_rels/.rels", docxPackageRels) ||
		!writeFile("word/styles.xml", docxStylesXML(setting)) ||
		!writeFile("word/_rels/document.xml.rels", rels.String()) ||
		!writeFile("word/document.xml", docxDocumentXML(r.body.String(), docxSectPr(setting))) {
		return
	}
	for _, name := range r.imageOrder {
		fw, err := w.Create("word/media/" + name)
		if err == nil {
			_, err = fw.Write(r.images[name])
		}
		if err != nil {
			fail(c, http.StatusInternalServerError, "打包失败: "+err.Error())
			return
		}
	}
	if err := w.Close(); err != nil {
		fail(c, http.StatusInternalServerError, "打包失败: "+err.Error())
		return
	}

	filename := book.Slug
	if filename == "" {
		filename = fmt.Sprintf("book-%d", book.ID)
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.docx", filename))
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", buf.Bytes())
}

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Default Extension="jpg" ContentType="image/jpeg"/><Default Extension="jpeg" ContentType="image/jpeg"/><Default Extension="gif" ContentType="image/gif"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`

const docxPackageRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

func docxDocumentXML(body, sectPr string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		"<w:body>" + body + sectPr + "</w:body></w:document>"
}

func docxSectPr(s models.UserExportSetting) string {
	pw, ph := 11906, 16838 // A4 twips
	if s.PageSize == "Letter" {
		pw, ph = 12240, 15840
	}
	margin := 1440
	switch s.Margin {
	case "narrow":
		margin = 720
	case "wide":
		margin = 1800
	}
	return fmt.Sprintf(`<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`, pw, ph, margin, margin, margin, margin)
}

func docxStylesXML(s models.UserExportSetting) string {
	sz := s.FontSize * 2 // 半磅
	if sz <= 0 {
		sz = 30
	}
	codeFill, codeColor := "F6F8FA", "24292E"
	if s.CodeTheme == "dark" {
		codeFill, codeColor = "0D1117", "C9D1D9"
	}
	heading := func(id, name string, level, hsz int) string {
		return fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="%s"><w:name w:val="%s"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="%d"/></w:pPr><w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`, id, name, level, hsz)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	b.WriteString(fmt.Sprintf(`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Microsoft YaHei"/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:rPrDefault></w:docDefaults>`, sz, sz))
	b.WriteString(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:style>`)
	b.WriteString(fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="480" w:after="240"/><w:jc w:val="center"/></w:pPr><w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`, sz*2))
	b.WriteString(heading("Heading1", "heading 1", 0, sz+16))
	b.WriteString(heading("Heading2", "heading 2", 1, sz+10))
	b.WriteString(heading("Heading3", "heading 3", 2, sz+6))
	b.WriteString(heading("Heading4", "heading 4", 3, sz+4))
	b.WriteString(heading("Heading5", "heading 5", 4, sz+2))
	b.WriteString(heading("Heading6", "heading 6", 5, sz))
	b.WriteString(`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="480"/><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="CBD5E1"/></w:pBdr></w:pPr><w:rPr><w:i/><w:color w:val="475569"/></w:rPr></w:style>`)
	b.WriteString(fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="%s"/><w:spacing w:before="60" w:after="60"/></w:pPr><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/><w:color w:val="%s"/><w:sz w:val="%d"/></w:rPr></w:style>`, codeFill, codeColor, sz-2))
	b.WriteString(`</w:styles>`)
	return b.String()
}
