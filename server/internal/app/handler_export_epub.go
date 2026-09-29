package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"net/http"
	"path"
	"sort"
	"strings"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

// epubMarkdown 以 GFM + XHTML 自闭合标签渲染，保证输出为合法 XHTML 片段（不放行裸 HTML）
var epubMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(ghtml.WithXHTML()),
)

// ExportBookEPUB GET /books/:id/export/epub 导出书籍为 EPUB 电子书
func (a *App) ExportBookEPUB(c *gin.Context) {
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
	if !formatAllowed(book, "epub") {
		fail(c, http.StatusForbidden, "作者未开放 EPUB 导出")
		return
	}
	a.recordBookExport(u, book, "epub")
	a.writeBookEPUB(c, book, a.canEditBookContent(u, book))
}

// epubImageMediaType 依扩展名返回 EPUB manifest 的媒体类型
func epubImageMediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func renderEPUBChapterBody(md string) string {
	var buf bytes.Buffer
	if err := epubMarkdown.Convert([]byte(md), &buf); err != nil {
		// 渲染失败时退化为转义纯文本，保证 XHTML 合法
		return "<p>" + html.EscapeString(md) + "</p>"
	}
	return buf.String()
}

func epubXHTMLDoc(title, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="zh" lang="zh">
<head>
<meta charset="utf-8"/>
<title>` + html.EscapeString(title) + `</title>
<link rel="stylesheet" type="text/css" href="styles.css"/>
</head>
<body>
` + body + `
</body>
</html>`
}

type epubChapter struct {
	title string
	file  string // OEBPS 内相对文件名
}

// writeBookEPUB 打包书籍为 EPUB 并写入响应（鉴权由调用方负责）。
// includeDrafts 为 true 时（作者/协作者）包含草稿章节，否则仅已发布章节。
func (a *App) writeBookEPUB(c *gin.Context, book *models.Book, includeDrafts bool) {
	setting := a.resolveExportStyle(c.Query("style"), book, currentUser(c))
	footer := a.resolveExportFooter(book, currentUser(c))

	docs := []models.Document{}
	if err := a.DB.Where("book_id = ?", book.ID).Order("sort_order ASC, id ASC").Find(&docs).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询章节失败")
		return
	}

	images := map[string][]byte{}
	// 封面图（仅嵌入本站已上传的封面）
	coverName := ""
	if book.CoverImage != "" {
		rewritten, files := a.rewriteUploadsToLocal(book.CoverImage)
		if strings.HasPrefix(rewritten, "images/") {
			coverName = strings.TrimPrefix(rewritten, "images/")
			for n, d := range files {
				images[n] = d
			}
		}
	}

	chapters := []epubChapter{}

	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)

	fail500 := func(err error) {
		fail(c, http.StatusInternalServerError, "打包失败: "+err.Error())
	}
	writeFile := func(name, content string) error {
		fw, err := w.Create(name)
		if err != nil {
			return err
		}
		_, err = fw.Write([]byte(content))
		return err
	}

	// 1. mimetype 必须为第一个条目且非压缩存储
	if mh, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store}); err != nil {
		fail500(err)
		return
	} else if _, err := mh.Write([]byte("application/epub+zip")); err != nil {
		fail500(err)
		return
	}

	// 2. container.xml
	if err := writeFile("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles>
<rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
</rootfiles>
</container>`); err != nil {
		fail500(err)
		return
	}

	// 3. 样式表（沿用导出样式：正文字号与代码配色）
	codeBg, codeFg := "#f6f8fa", "#24292e"
	if setting.CodeTheme == "dark" {
		codeBg, codeFg = "#0d1117", "#c9d1d9"
	}
	styles := fmt.Sprintf(`body{font-family:-apple-system,"Segoe UI",Roboto,"Helvetica Neue","PingFang SC","Microsoft YaHei",sans-serif;font-size:%dpx;line-height:1.7;color:#1f2937;margin:1em;}
h1,h2,h3,h4{line-height:1.3;margin:1.2em 0 .6em;}
h1.chapter-title{border-bottom:1px solid #e5e7eb;padding-bottom:.3em;}
p{margin:.7em 0;}
img{max-width:100%%;height:auto;}
pre{background:%s;color:%s;padding:1em;border-radius:6px;overflow:auto;font-size:.9em;}
code{font-family:"SFMono-Regular",Consolas,"Liberation Mono",monospace;}
pre code{background:transparent;padding:0;}
:not(pre)>code{background:#f1f5f9;padding:.1em .35em;border-radius:4px;font-size:.9em;}
blockquote{margin:.8em 0;padding:.2em 1em;border-left:4px solid #cbd5e1;color:#475569;}
table{border-collapse:collapse;margin:1em 0;width:100%%;}
th,td{border:1px solid #cbd5e1;padding:.4em .6em;}
.cover{text-align:center;}
.cover img{max-height:90vh;}
.book-title{text-align:center;font-size:1.8em;margin-top:2em;}
.book-author{text-align:center;color:#6b7280;}
.footer,.watermark{margin-top:2em;text-align:center;color:#9ca3af;font-size:.8em;}
nav ol{list-style:none;padding-left:0;}
nav li{margin:.4em 0;}`, setting.FontSize, codeBg, codeFg)
	if err := writeFile("OEBPS/styles.css", styles); err != nil {
		fail500(err)
		return
	}

	manifest := &strings.Builder{}
	spine := &strings.Builder{}
	navList := &strings.Builder{}

	// 4. 封面页（有上传封面时）
	if setting.IncludeCover {
		var coverBody string
		if coverName != "" {
			coverBody = `<div class="cover"><img src="images/` + html.EscapeString(coverName) + `" alt="` + html.EscapeString(book.Title) + `"/></div>`
		} else {
			author := ""
			if book.User != nil {
				author = book.User.Username
			}
			coverBody = `<div class="cover"><h1 class="book-title">` + html.EscapeString(book.Title) + `</h1>`
			if author != "" {
				coverBody += `<p class="book-author">` + html.EscapeString(author) + `</p>`
			}
			coverBody += `</div>`
		}
		if err := writeFile("OEBPS/cover.xhtml", epubXHTMLDoc(book.Title, coverBody)); err != nil {
			fail500(err)
			return
		}
		manifest.WriteString(`<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="cover"/>`)
	}

	// 5. 章节
	idx := 0
	for _, doc := range docs {
		if !includeDrafts && doc.Status != "published" {
			continue
		}
		idx++
		content, docImages := a.rewriteUploadsToLocal(docBody(doc.Content)) // 开头的章节元数据不导出
		for n, d := range docImages {
			images[n] = d
		}
		body := `<h1 class="chapter-title">` + html.EscapeString(doc.Title) + `</h1>` + "\n" + renderEPUBChapterBody(content)
		if a.pluginEnabled(pluginWatermark) && book.WatermarkEnabled && strings.TrimSpace(book.WatermarkText) != "" {
			body += `<p class="watermark">` + html.EscapeString(book.WatermarkText) + `</p>`
		}
		file := fmt.Sprintf("chapter-%04d.xhtml", idx)
		if err := writeFile("OEBPS/"+file, epubXHTMLDoc(doc.Title, body)); err != nil {
			fail500(err)
			return
		}
		id := fmt.Sprintf("chap%04d", idx)
		manifest.WriteString(`<item id="` + id + `" href="` + file + `" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="` + id + `"/>`)
		navList.WriteString(`<li><a href="` + file + `">` + html.EscapeString(doc.Title) + `</a></li>`)
		chapters = append(chapters, epubChapter{title: doc.Title, file: file})
	}

	// 6. 图片资源（稳定顺序，便于可复现打包）
	imageNames := make([]string, 0, len(images))
	for n := range images {
		imageNames = append(imageNames, n)
	}
	sort.Strings(imageNames)
	for i, name := range imageNames {
		fw, err := w.Create("OEBPS/images/" + name)
		if err != nil {
			fail500(err)
			return
		}
		if _, err := fw.Write(images[name]); err != nil {
			fail500(err)
			return
		}
		props := ""
		if name == coverName {
			props = ` properties="cover-image"`
		}
		manifest.WriteString(fmt.Sprintf(`<item id="img%d" href="images/%s" media-type="%s"%s/>`,
			i, html.EscapeString(name), epubImageMediaType(name), props))
	}

	// 7. nav.xhtml（EPUB3 目录）
	navBody := `<nav epub:type="toc" id="toc"><h1>目录</h1><ol>` + navList.String() + `</ol></nav>`
	if setting.IncludeToc {
		navBody += `<p class="footer">` + html.EscapeString(footer) + `</p>`
	}
	navDoc := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="zh" lang="zh">
<head><meta charset="utf-8"/><title>目录</title><link rel="stylesheet" type="text/css" href="styles.css"/></head>
<body>` + navBody + `</body></html>`
	if err := writeFile("OEBPS/nav.xhtml", navDoc); err != nil {
		fail500(err)
		return
	}
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	if setting.IncludeToc {
		spine.WriteString(`<itemref idref="nav"/>`)
	}

	// 8. toc.ncx（EPUB2 兼容）
	navPoints := &strings.Builder{}
	for i, ch := range chapters {
		navPoints.WriteString(fmt.Sprintf(
			`<navPoint id="np%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/></navPoint>`,
			i+1, i+1, html.EscapeString(ch.title), ch.file))
	}
	bookID := book.Slug
	if bookID == "" {
		bookID = fmt.Sprintf("book-%d", book.ID)
	}
	ncx := `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="urn:knowforge:` + html.EscapeString(bookID) + `"/></head>
<docTitle><text>` + html.EscapeString(book.Title) + `</text></docTitle>
<navMap>` + navPoints.String() + `</navMap>
</ncx>`
	if err := writeFile("OEBPS/toc.ncx", ncx); err != nil {
		fail500(err)
		return
	}
	manifest.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)

	// 9. content.opf
	author := ""
	if book.User != nil {
		author = book.User.Username
	}
	coverMeta := ""
	if coverName != "" {
		coverMeta = `<meta name="cover" content="` + fmt.Sprintf("img%d", indexOf(imageNames, coverName)) + `"/>`
	}
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="bookid">urn:knowforge:` + html.EscapeString(bookID) + `</dc:identifier>
<dc:title>` + html.EscapeString(book.Title) + `</dc:title>
<dc:language>zh</dc:language>
<dc:creator>` + html.EscapeString(author) + `</dc:creator>
<dc:description>` + html.EscapeString(book.Description) + `</dc:description>
` + coverMeta + `
</metadata>
<manifest>` + manifest.String() + `</manifest>
<spine toc="ncx">` + spine.String() + `</spine>
</package>`
	if err := writeFile("OEBPS/content.opf", opf); err != nil {
		fail500(err)
		return
	}

	if err := w.Close(); err != nil {
		fail500(err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.epub", bookID))
	c.Data(http.StatusOK, "application/epub+zip", buf.Bytes())
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}
