package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"knowforge/server/internal/auth"
	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/testdb"
)

// 2x1 图片：左红右蓝。
func sampleImage() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(1, 0, color.NRGBA{0, 0, 255, 255})
	return img
}

func TestTransformImage(t *testing.T) {
	red, blue := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 0, 255, 255}
	at := func(img image.Image, x, y int) color.NRGBA {
		return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	}
	out, ok := transformImage(sampleImage(), imageEditRequest{Rotate: 90})
	if !ok || out.Bounds().Dx() != 1 || out.Bounds().Dy() != 2 || at(out, 0, 0) != red || at(out, 0, 1) != blue {
		t.Fatalf("顺时针 90° 后应为上红下蓝: %v", out.Bounds())
	}
	out, _ = transformImage(sampleImage(), imageEditRequest{FlipH: true})
	if at(out, 0, 0) != blue {
		t.Fatal("水平翻转后左侧应为蓝")
	}
	out, _ = transformImage(sampleImage(), imageEditRequest{Rotate: 270, FlipV: true})
	if at(out, 0, 0) != red {
		t.Fatal("逆时针 90° 再垂直翻转后顶部应为红")
	}
	out, ok = transformImage(sampleImage(), imageEditRequest{Crop: &struct{ X, Y, W, H int }{1, 0, 5, 5}})
	if !ok || out.Bounds().Dx() != 1 || at(out, 0, 0) != blue {
		t.Fatal("裁剪应限定在图片范围内")
	}
	if _, ok := transformImage(sampleImage(), imageEditRequest{Rotate: 45}); ok {
		t.Fatal("只支持 90° 的倍数")
	}
}

// 编辑另存为新文件：原文件不变，新文件计入「我的文件」；他人的文件不可编辑。
func TestEditMyFile(t *testing.T) {
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Router())
	defer server.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	do := func(token, method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		p := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		return resp.StatusCode, p
	}
	do("", http.MethodPost, "/api/v1/setup/install", `{"database":`+testdb.InstallJSON(t)+`,"site":{"name":"编辑"},"admin":{"username":"ie-admin","email":"ie-admin@test.local","password":"secret123"}}`)
	newUser := func(name string) (*models.User, string) {
		u := &models.User{Username: name, Email: name + "@test.local", Role: "user", IsActive: true, EmailVerified: true}
		a.DB.Create(u)
		token, _ := auth.GenerateToken(a.Config.Secret, u.ID, u.Username, u.Role)
		return u, token
	}
	u, token := newUser("ie-user")
	_, other := newUser("ie-other")
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, sampleImage())
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", "a.png")
	_, _ = part.Write(pngBuf.Bytes())
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, err := client.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("上传失败: %v", err)
	}
	var original models.UserFile
	a.DB.Where("user_id = ?", u.ID).First(&original)

	path := fmt.Sprintf("/api/v1/users/me/files/%d/edit", original.ID)
	if status, _ := do(other, http.MethodPost, path, `{"rotate":90}`); status != http.StatusNotFound {
		t.Fatalf("不能编辑他人的文件: %d", status)
	}
	status, p := do(token, http.MethodPost, path, `{"rotate":90,"crop":{"x":0,"y":1,"w":1,"h":1}}`)
	if status != http.StatusOK {
		t.Fatalf("编辑失败: %d %v", status, p)
	}
	file := p["data"].(map[string]any)["file"].(map[string]any)
	if file["source"] != "edit" || file["url"] == original.URL || file["ext"] != "png" {
		t.Fatalf("应另存为新文件: %v", file)
	}
	var count int64
	a.DB.Model(&models.UserFile{}).Where("user_id = ?", u.ID).Count(&count)
	if count != 2 {
		t.Fatalf("原文件应保留、新文件计入: %d", count)
	}
	if status, _ := do(token, http.MethodPost, path, `{"rotate":30}`); status != http.StatusBadRequest {
		t.Fatalf("无效角度应 400: %d", status)
	}
}
