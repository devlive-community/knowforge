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

// EPUB 导出应产出结构合法的 EPUB：mimetype 首位且非压缩、含 container/opf/nav 与章节。
func TestExportBookEPUBStructure(t *testing.T) {
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
	book := models.Book{Title: "测试书", Slug: "test-book", UserID: admin.ID, Status: "published", IsPublic: true, ExportEnabled: true}
	if err := a.DB.Create(&book).Error; err != nil {
		t.Fatalf("创建书籍失败: %v", err)
	}
	doc := models.Document{BookID: book.ID, UserID: admin.ID, Title: "第一章", Slug: "ch1", Content: "# 标题\n\n正文 **加粗**。\n", Status: "published", SortOrder: 1}
	if err := a.DB.Create(&doc).Error; err != nil {
		t.Fatalf("创建章节失败: %v", err)
	}

	er, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/books/"+itoa(book.ID)+"/export/epub", nil)
	er2, err := http.DefaultClient.Do(er)
	if err != nil {
		t.Fatalf("导出请求失败: %v", err)
	}
	defer er2.Body.Close()
	if er2.StatusCode != http.StatusOK {
		t.Fatalf("EPUB 导出应 200，实际 %d", er2.StatusCode)
	}
	if ct := er2.Header.Get("Content-Type"); !strings.Contains(ct, "application/epub+zip") {
		t.Fatalf("Content-Type 应为 epub，实际 %q", ct)
	}
	body, _ := io.ReadAll(er2.Body)

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("EPUB 不是合法 zip: %v", err)
	}
	// mimetype 必须为第一个条目、非压缩、内容正确
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Fatalf("首个条目应为 mimetype")
	}
	if zr.File[0].Method != zip.Store {
		t.Fatalf("mimetype 必须以 Store 非压缩存储")
	}
	rc, _ := zr.File[0].Open()
	mt, _ := io.ReadAll(rc)
	rc.Close()
	if string(mt) != "application/epub+zip" {
		t.Fatalf("mimetype 内容错误：%q", mt)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/toc.ncx", "OEBPS/chapter-0001.xhtml"} {
		if !names[want] {
			t.Fatalf("EPUB 缺少 %s", want)
		}
	}

	// 所有 xml/xhtml/opf/ncx 必须是良构 XML（EPUB 阅读器严格解析）
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".xhtml") && !strings.HasSuffix(f.Name, ".opf") &&
			!strings.HasSuffix(f.Name, ".ncx") && !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s 不是良构 XML: %v", f.Name, err)
			}
		}
	}
}

func itoa(v uint) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
