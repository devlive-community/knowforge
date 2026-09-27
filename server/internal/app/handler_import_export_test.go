package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"knowforge/server/internal/config"
	"knowforge/server/internal/testdb"
)

// M16 导入导出集成测试：验收标准 = 导出再导入内容无损
func TestBookExportImport(t *testing.T) {
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

	client := &http.Client{Timeout: 10 * 1e9}
	request := func(method, path string, body any, token string) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	// 安装 + alice/bob 注册
	_, install := request(http.MethodPost, "/api/v1/setup/install", map[string]any{
		"database": testdb.InstallMap(t),
		"site":     map[string]any{"name": "导入导出测试站"},
		"admin":    map[string]any{"username": "admin", "email": "admin@test.local", "password": "secret123"},
	}, "")
	adminToken := install["data"].(map[string]any)["token"].(string)
	register := func(username string) string {
		_, reg := request(http.MethodPost, "/api/v1/auth/register", map[string]any{
			"username": username, "email": username + "@test.local", "password": "secret123",
		}, "")
		return reg["data"].(map[string]any)["token"].(string)
	}
	aliceToken := register("alice")
	bobToken := register("bob")

	// 上传一张图片，作为封面与正文配图
	fakePNG := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 64)
	uploadBody := &bytes.Buffer{}
	mw := multipart.NewWriter(uploadBody)
	fw, _ := mw.CreateFormFile("file", "20260906-test.png")
	_, _ = fw.Write(fakePNG)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/upload", uploadBody)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	upResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	var upPayload map[string]any
	_ = json.NewDecoder(upResp.Body).Decode(&upPayload)
	upResp.Body.Close()
	if upResp.StatusCode != 200 {
		t.Fatalf("上传返回 %d: %v", upResp.StatusCode, upPayload)
	}
	imageName := filepath.Base(upPayload["data"].(map[string]any)["url"].(string))
	imageURL := "/uploads/" + imageName

	// 建书：标签 + 封面 + 三个章节（含子章节与草稿）
	status, book := request(http.MethodPost, "/api/v1/books", map[string]any{
		"title": "便携之书", "description": "带回车的\n描述: 冒号", "status": "published", "is_public": true,
		"tags": []any{"Go", "测试"}, "watermark_enabled": true, "watermark_text": "Alice · 内部资料",
	}, aliceToken)
	bookData := book["data"].(map[string]any)
	bookID := int(bookData["id"].(float64))
	request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{
		"cover_image": imageURL,
	}, aliceToken)
	// 更多信息：非法链接/自定义项缺名称 400；合法项保存（空值项丢弃）
	for _, bad := range []any{
		[]any{map[string]any{"type": "github", "value": "not-a-url"}},
		[]any{map[string]any{"type": "custom", "value": "x"}},
		[]any{map[string]any{"type": "email", "value": "bad@"}},
		[]any{map[string]any{"type": "unknown", "value": "x"}},
	} {
		if s, _ := request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{"extra_info": bad}, aliceToken); s != http.StatusBadRequest {
			t.Fatalf("非法更多信息应 400: %v → %d", bad, s)
		}
	}
	extraInfo := []any{
		map[string]any{"type": "github", "value": "https://github.com/devlive-community/knowforge"},
		map[string]any{"type": "license", "value": "MIT"},
		map[string]any{"type": "custom", "label": "交流群", "value": "QQ 123456: 欢迎"},
		map[string]any{"type": "docs", "value": "  "},
	}
	if s, p := request(http.MethodPut, fmt.Sprintf("/api/v1/books/%d", bookID), map[string]any{"extra_info": extraInfo}, aliceToken); s != 200 || len(p["data"].(map[string]any)["extra_info"].([]any)) != 3 {
		t.Fatalf("保存更多信息失败: %d %v", s, p)
	}
	mkDoc := func(payload map[string]any) int {
		t.Helper()
		s, d := request(http.MethodPost, fmt.Sprintf("/api/v1/books/%d/documents", bookID), payload, aliceToken)
		if s != 200 {
			t.Fatalf("创建章节失败: %d %v", s, d)
		}
		return int(d["data"].(map[string]any)["id"].(float64))
	}
	doc1 := mkDoc(map[string]any{
		"title": "第一章", "slug": "chapter-one", "status": "published",
		"content": "正文开始\n\n![配图](" + imageURL + ")\n\n图片后内容",
	})
	doc2 := mkDoc(map[string]any{
		"title": "第二章", "slug": "chapter-two", "status": "draft", "sort_order": 5,
		"content": "草稿内容: 带冒号",
	})
	doc3 := mkDoc(map[string]any{
		"title": "子章节", "slug": "chapter-one-sub", "status": "published", "parent_id": doc1, "allow_comments": false,
		"content": "子章节正文",
	})
	_ = doc3

	// 1. 权限：bob 导出他人书籍 → 403；未登录 → 401
	status, _ = request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/export", bookID), nil, "")
	if status != 401 {
		t.Fatalf("未登录导出应 401: %d", status)
	}
	exportGet := func(token string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+fmt.Sprintf("/api/v1/books/%d/export", bookID), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("导出请求失败: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}
	status, _ = exportGet(bobToken)
	if status != 403 {
		t.Fatalf("非所有者导出应 403: %d", status)
	}
	status, raw := exportGet(aliceToken)
	if status != 200 || !bytes.Equal(raw[:2], []byte("PK")) {
		t.Fatalf("导出应为 zip: %d %x", status, raw[:2])
	}

	// 2. 校验 zip 结构：book.md / 章节 / 图片；uploads 引用已改写为 images/
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("zip 解析失败: %v", err)
	}
	zf := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		zf[f.Name] = data
	}
	bookMD := string(zf["book.md"])
	if !strings.Contains(bookMD, `"便携之书"`) || !strings.Contains(bookMD, "[Go, 测试]") ||
		!strings.Contains(bookMD, `"images/`+imageName+`"`) {
		t.Fatalf("book.md 内容异常:\n%s", bookMD)
	}
	chapterCount := 0
	for name := range zf {
		if strings.HasPrefix(name, "chapters/") && strings.HasSuffix(name, ".md") {
			chapterCount++
		}
	}
	if chapterCount != 3 {
		t.Fatalf("应打包 3 个章节: %d", chapterCount)
	}
	if !bytes.Equal(zf["images/"+imageName], fakePNG) {
		t.Fatalf("图片应随包携带")
	}
	var doc1File string
	for name, data := range zf {
		if strings.HasPrefix(name, "chapters/") && strings.Contains(string(data), "![配图](") {
			doc1File = name
			if !strings.Contains(string(data), "](images/"+imageName+")") || strings.Contains(string(data), "](/uploads/") {
				t.Fatalf("正文图片引用应改写为包内路径: %q", string(data))
			}
		}
	}
	if doc1File == "" {
		t.Fatal("找不到带配图的章节")
	}

	// 3. 导入：zip → 新书（slug 冲突自动重生成）
	importZip := func(token string, raw []byte) (int, map[string]any) {
		t.Helper()
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		fw, _ := mw.CreateFormFile("file", "portable.zip")
		_, _ = fw.Write(raw)
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/import", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("导入请求失败: %v", err)
		}
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		if resp.StatusCode != http.StatusAccepted {
			return resp.StatusCode, payload
		}
		taskID := uint(payload["data"].(map[string]any)["task"].(map[string]any)["id"].(float64))
		if ran, runErr := a.Jobs.RunOnce(context.Background()); !ran || runErr != nil {
			t.Fatalf("ZIP 后台导入失败: ran=%v err=%v", ran, runErr)
		}
		status, taskPayload := request(http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", taskID), nil, token)
		if status != http.StatusOK {
			return status, taskPayload
		}
		result := taskPayload["data"].(map[string]any)["result"]
		return http.StatusOK, map[string]any{"success": true, "data": result}
	}
	status, imported := importZip(aliceToken, raw)
	if status != 200 {
		t.Fatalf("导入失败: %d %v", status, imported)
	}
	importedBook := imported["data"].(map[string]any)["book"].(map[string]any)
	if importedBook["slug"].(string) == bookData["slug"].(string) {
		t.Fatalf("导入 slug 冲突应自动重生成: %v", importedBook["slug"])
	}
	if imported["data"].(map[string]any)["imported_doc"].(float64) != 3 {
		t.Fatalf("应导入 3 个章节: %v", imported)
	}
	newBookID := int(importedBook["id"].(float64))

	// 4. 无损校验：元数据、标签、封面、章节树、正文图片引用
	status, got := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d", newBookID), nil, aliceToken)
	if status != 200 {
		t.Fatalf("导入书不可读: %d", status)
	}
	gotBook := got["data"].(map[string]any)
	if gotBook["title"] != "便携之书" || gotBook["description"] != "带回车的\n描述: 冒号" ||
		gotBook["cover_image"] != imageURL || gotBook["is_public"] != true ||
		gotBook["watermark_enabled"] != true || gotBook["watermark_text"] != "Alice · 内部资料" {
		t.Fatalf("书籍元数据未无损还原: %v", gotBook)
	}
	if len(gotBook["tags"].([]any)) != 2 {
		t.Fatalf("标签未还原: %v", gotBook["tags"])
	}
	if info := gotBook["extra_info"].([]any); len(info) != 3 || info[2].(map[string]any)["label"] != "交流群" || info[2].(map[string]any)["value"] != "QQ 123456: 欢迎" {
		t.Fatalf("更多信息未还原: %v", gotBook["extra_info"])
	}
	status, tree := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents", newBookID), nil, aliceToken)
	docs := tree["data"].([]any)
	if len(docs) != 2 {
		t.Fatalf("顶级章节应为 2（子章节挂第一章）: %v", tree)
	}
	// 4.1 读取导入后的第一章，验证图片引用与子章节还原
	_, docDetail := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents/slug/chapter-one", newBookID), nil, aliceToken)
	importedDoc1 := docDetail["data"].(map[string]any)
	if !strings.Contains(importedDoc1["content"].(string), "![配图]("+imageURL+")") {
		t.Fatalf("导入后图片引用应还原为 /uploads: %q", importedDoc1["content"])
	}
	for _, node := range docs {
		nodeMap := node.(map[string]any)
		if nodeMap["slug"] != "chapter-one" {
			continue
		}
		children := nodeMap["children"].([]any)
		if len(children) != 1 || children[0].(map[string]any)["title"] != "子章节" {
			t.Fatalf("子章节未还原: %v", children)
		}
	}
	// 树接口不含 allow_comments，改从章节详情校验
	_, childDetail := request(http.MethodGet, fmt.Sprintf("/api/v1/books/%d/documents/slug/chapter-one-sub", newBookID), nil, aliceToken)
	child := childDetail["data"].(map[string]any)
	if child["allow_comments"] != false {
		t.Fatalf("allow_comments 未还原: %v", child)
	}
	if child["content"] != "子章节正文" {
		t.Fatalf("子章节正文未还原: %v", child["content"])
	}
	if _, err := os.Stat(filepath.Join(uploadsDir(), imageName)); err != nil {
		t.Fatalf("上传目录中的原图应始终存在: %v", err)
	}

	// 5. 再次导入同一 zip：slug 再次重生成，幂等可用
	status, second := importZip(aliceToken, raw)
	if status != 200 {
		t.Fatalf("二次导入失败: %d %v", status, second)
	}
	if second["data"].(map[string]any)["book"].(map[string]any)["slug"].(string) == importedBook["slug"].(string) {
		t.Fatalf("二次导入 slug 应再次重生成")
	}

	// 6. 导入权限：未登录 → 401
	status, _ = importZip("", raw)
	if status != 401 {
		t.Fatalf("未登录导入应 401: %d", status)
	}
	_ = adminToken
	_ = doc2
}
