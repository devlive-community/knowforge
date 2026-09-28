package booklists_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"knowforge/server/internal/app"
	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 书单：创建（受权益数量限制）、收录与推荐语、排序、只展示查看者可读的书；私有书单他人不可见；
// 书单广场只含有书的公开书单并按收藏数排序；书籍详情列出收录它的书单；收藏与取消收藏。
func TestBookLists(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	data := func(p map[string]any) map[string]any { d, _ := p["data"].(map[string]any); return d }
	items := func(p map[string]any) []map[string]any {
		out := []map[string]any{}
		for _, it := range data(p)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	_, installed := do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"书单站"},"admin":{"username":"bls-admin","email":"bls-admin@test.local","password":"secret123"}}`)
	adminToken := data(installed)["token"].(string)
	user := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		tok, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, tok
	}
	_, curator := user("bls-curator")
	_, author := user("bls-author")
	_, reader := user("bls-reader")
	newBook := func(title string, public bool) uint {
		_, p := do(author, http.MethodPost, "/api/v1/books", fmt.Sprintf(`{"title":%q,"status":"published","is_public":%v}`, title, public))
		return uint(data(p)["id"].(float64))
	}
	b1, b2, b3 := newBook("甲书", true), newBook("乙书", true), newBook("丙书", true)
	private := newBook("私密书", false)

	if status, _ := do(curator, http.MethodGet, "/api/v1/book-lists/mine", ""); status != http.StatusNotFound {
		t.Fatalf("插件未启用时应不可用: %d", status)
	}
	if status, p := do(adminToken, http.MethodPost, "/api/v1/admin/plugins/book-lists/install", ""); status != http.StatusOK {
		t.Fatalf("启用插件失败: %d %v", status, p)
	}

	// 创建时可同时收录一本书
	status, p := do(curator, http.MethodPost, "/api/v1/book-lists", fmt.Sprintf(`{"title":"  入门必读  ","description":"新手先看","book_id":%d}`, b1))
	if status != http.StatusOK || data(p)["title"] != "入门必读" || data(p)["item_count"].(float64) != 1 || data(p)["is_public"] != true {
		t.Fatalf("创建书单失败: %d %v", status, p)
	}
	listID := uint(data(p)["id"].(float64))
	base := fmt.Sprintf("/api/v1/book-lists/%d", listID)
	if status, _ := do(curator, http.MethodPost, base+"/items", fmt.Sprintf(`{"book_id":%d,"note":"最好的入门"}`, b2)); status != http.StatusOK {
		t.Fatalf("收录失败: %d", status)
	}
	do(curator, http.MethodPost, base+"/items", fmt.Sprintf(`{"book_id":%d}`, b3))
	if status, _ := do(curator, http.MethodPost, base+"/items", fmt.Sprintf(`{"book_id":%d}`, b2)); status != http.StatusConflict {
		t.Fatalf("重复收录应冲突: %d", status)
	}
	if status, _ := do(curator, http.MethodPost, base+"/items", fmt.Sprintf(`{"book_id":%d}`, private)); status != http.StatusNotFound {
		t.Fatalf("不可读的书不能收录: %d", status)
	}
	if status, _ := do(reader, http.MethodPost, base+"/items", fmt.Sprintf(`{"book_id":%d}`, b3)); status != http.StatusForbidden {
		t.Fatalf("他人不能修改书单: %d", status)
	}
	// 作者自己的书单收录私有书：他人看不到，作者看到 hidden 提示之外的全部
	_, ap := do(author, http.MethodPost, "/api/v1/book-lists", fmt.Sprintf(`{"title":"作者书单","book_id":%d}`, private))
	authorList := uint(data(ap)["id"].(float64))
	do(author, http.MethodPost, fmt.Sprintf("/api/v1/book-lists/%d/items", authorList), fmt.Sprintf(`{"book_id":%d}`, b1))
	_, dp := do("", http.MethodGet, fmt.Sprintf("/api/v1/book-lists/%d", authorList), "")
	if got := data(dp)["items"].([]any); len(got) != 1 || data(dp)["hidden"].(float64) != 0 {
		t.Fatalf("游客只应看到可读的书且不显示隐藏数: %v", data(dp))
	}
	a.DB.Model(&models.Book{}).Where("id = ?", b3).Update("is_public", false)
	_, dp = do(curator, http.MethodGet, base, "")
	if got := data(dp)["items"].([]any); len(got) != 2 || data(dp)["hidden"].(float64) != 1 || data(dp)["mine"] != true {
		t.Fatalf("书籍改为私有后创建者应看到隐藏数: %v", data(dp))
	}
	a.DB.Model(&models.Book{}).Where("id = ?", b3).Update("is_public", true)

	// 推荐语与排序
	do(curator, http.MethodPut, fmt.Sprintf("%s/items/%d", base, b1), `{"note":"经典"}`)
	do(curator, http.MethodPut, base+"/order", fmt.Sprintf(`{"book_ids":[%d,%d]}`, b3, b1))
	_, dp = do("", http.MethodGet, base, "")
	order := []string{}
	for _, it := range data(dp)["items"].([]any) {
		m := it.(map[string]any)
		order = append(order, m["book"].(map[string]any)["title"].(string)+":"+m["note"].(string))
	}
	if fmt.Sprint(order) != "[丙书: 甲书:经典 乙书:最好的入门]" {
		t.Fatalf("排序或推荐语异常: %v", order)
	}

	// 私有书单他人不可见
	_, pp := do(curator, http.MethodPost, "/api/v1/book-lists", `{"title":"私人收藏","is_public":false}`)
	privList := uint(data(pp)["id"].(float64))
	if status, _ := do(reader, http.MethodGet, fmt.Sprintf("/api/v1/book-lists/%d", privList), ""); status != http.StatusNotFound {
		t.Fatalf("私有书单他人不可见: %d", status)
	}
	_, up := do(reader, http.MethodGet, "/api/v1/users/bls-curator/book-lists", "")
	if got := items(up); len(got) != 1 || got[0]["title"] != "入门必读" || len(got[0]["covers"].([]any)) != 3 {
		t.Fatalf("个人主页只应列出公开书单: %v", got)
	}
	_, up = do(curator, http.MethodGet, "/api/v1/users/bls-curator/book-lists", "")
	if len(items(up)) != 2 {
		t.Fatalf("本人应看到全部书单: %v", items(up))
	}

	// 收藏：广场按收藏数排序，空书单不出现
	if status, _ := do(curator, http.MethodPost, base+"/follow", ""); status != http.StatusBadRequest {
		t.Fatalf("不能收藏自己的书单: %d", status)
	}
	if status, fp := do(reader, http.MethodPost, base+"/follow", ""); status != http.StatusOK || data(fp)["follower_count"].(float64) != 1 {
		t.Fatalf("收藏失败: %d %v", status, fp)
	}
	do(reader, http.MethodPost, base+"/follow", "")
	_, gp := do("", http.MethodGet, "/api/v1/book-lists", "")
	got := items(gp)
	if len(got) != 2 || got[0]["title"] != "入门必读" || got[0]["follower_count"].(float64) != 1 {
		t.Fatalf("书单广场异常: %v", got)
	}
	_, fl := do(reader, http.MethodGet, "/api/v1/book-lists/followed", "")
	if f := items(fl); len(f) != 1 || f[0]["following"] != true {
		t.Fatalf("我收藏的书单异常: %v", f)
	}

	// 书籍详情：收录它的公开书单；我的书单标注是否已收录
	_, bp := do("", http.MethodGet, fmt.Sprintf("/api/v1/books/%d/book-lists", b1), "")
	if data(bp)["total"].(float64) != 2 {
		t.Fatalf("收录甲书的公开书单应有 2 个: %v", data(bp))
	}
	_, mp := do(curator, http.MethodGet, fmt.Sprintf("/api/v1/book-lists/mine?book_id=%d", b2), "")
	for _, l := range items(mp) {
		if want := l["title"] == "入门必读"; l["contains"] != want {
			t.Fatalf("是否已收录标注错误: %v", l)
		}
	}

	// 移出与删除
	do(curator, http.MethodDelete, fmt.Sprintf("%s/items/%d", base, b2), "")
	_, dp = do(curator, http.MethodGet, base, "")
	if data(dp)["list"].(map[string]any)["item_count"].(float64) != 2 {
		t.Fatalf("移出后收录数应为 2: %v", data(dp)["list"])
	}
	do(curator, http.MethodDelete, base, "")
	if status, _ := do(reader, http.MethodGet, base, ""); status != http.StatusNotFound {
		t.Fatalf("删除后应不存在: %d", status)
	}
	_, fl = do(reader, http.MethodGet, "/api/v1/book-lists/followed", "")
	if len(items(fl)) != 0 {
		t.Fatalf("删除后收藏应一并清除: %v", items(fl))
	}

	// 书单数量权益
	do(adminToken, http.MethodPut, "/api/v1/admin/entitlements/base", `{"values":{"booklists.max":1}}`)
	if status, _ := do(curator, http.MethodPost, "/api/v1/book-lists", `{"title":"第二个"}`); status != http.StatusForbidden {
		t.Fatalf("超过书单数量上限应拒绝: %d", status)
	}
}
