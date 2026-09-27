package app

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// DOCX 导出应产出结构合法、良构 XML 的 .docx（Word 严格解析 OOXML）。
func TestExportBookDOCXStructure(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	ts := httptest.NewServer(a.Router())
	defer ts.Close()

	install := `{"database":` + testdb.InstallJSON(t) + `,"site":{"name":"t"},"admin":{"username":"admin","email":"a@b.c","password":"secret123"}}`
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/setup/install", bytes.NewBufferString(install))
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("安装请求失败: %v", err)
	}
	resp.Body.Close()

	var admin models.User
	if err := a.DB.First(&admin).Error; err != nil {
		t.Fatalf("查询管理员失败: %v", err)
	}
	book := models.Book{Title: "测试书 & <特殊>", Slug: "docx-book", UserID: admin.ID, Status: "published", IsPublic: true, ExportEnabled: true}
	if err := a.DB.Create(&book).Error; err != nil {
		t.Fatalf("创建书籍失败: %v", err)
	}
	// 覆盖：标题/加粗/斜体/内联代码/列表/引用/代码块/表格/链接/特殊字符
	content := "# 一级标题\n\n正文 **加粗** 和 *斜体*，还有 `x<y` 与 & 符号。\n\n" +
		"- 项目一\n- 项目二\n  - 子项\n\n> 引用块\n\n```go\nfmt.Println(1 < 2)\n```\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n\n[链接](https://example.com)\n"
	doc := models.Document{BookID: book.ID, UserID: admin.ID, Title: "第一章 <x>", Slug: "ch1", Content: content, Status: "published", SortOrder: 1}
	if err := a.DB.Create(&doc).Error; err != nil {
		t.Fatalf("创建章节失败: %v", err)
	}

	er, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/books/"+itoa(book.ID)+"/export/docx", nil)
	er2, err := http.DefaultClient.Do(er)
	if err != nil {
		t.Fatalf("导出请求失败: %v", err)
	}
	defer er2.Body.Close()
	if er2.StatusCode != http.StatusOK {
		t.Fatalf("DOCX 导出应 200，实际 %d", er2.StatusCode)
	}
	if ct := er2.Header.Get("Content-Type"); !strings.Contains(ct, "wordprocessingml.document") {
		t.Fatalf("Content-Type 应为 docx，实际 %q", ct)
	}
	body, _ := io.ReadAll(er2.Body)

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("DOCX 不是合法 zip: %v", err)
	}
	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = data
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/_rels/document.xml.rels"} {
		if _, ok := parts[want]; !ok {
			t.Fatalf("DOCX 缺少 %s", want)
		}
	}

	// 所有 xml 部件必须良构（含特殊字符 & < 已转义）
	for name, data := range parts {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s 不是良构 XML: %v", name, err)
			}
		}
	}

	docXML := string(parts["word/document.xml"])
	for _, want := range []string{
		`w:pStyle w:val="Heading1"`, // 章节标题 + 一级标题
		`w:pStyle w:val="Code"`,     // 代码块
		"<w:tbl>",                   // 表格
		"<w:b/>",                    // 加粗
		"加粗",
		"example.com", // 链接 URL 追加
	} {
		if !strings.Contains(docXML, want) {
			t.Fatalf("document.xml 缺少 %q", want)
		}
	}
	// 特殊字符必须转义，不得出现裸 < 破坏 XML（正文里的 x<y）
	if strings.Contains(docXML, "x<y") {
		t.Fatalf("正文特殊字符未转义")
	}
}
