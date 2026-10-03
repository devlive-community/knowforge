package app

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif" // 注册 GIF 解码（PNG、JPEG 已随编码包注册）
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/storage"
)

// —— 「我的文件」中的图片编辑：旋转、翻转、裁剪后另存为新图片（原图与其引用不变，新图片计入个人存储）。
// 在服务端处理，避免浏览器画布读取跨域（如对象存储）图片的限制；支持 PNG、JPEG、GIF（GIF 取第一帧，保存为 PNG）。——

const maxEditPixels = 40_000_000 // 4000 万像素以内

var editableImageExts = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true}

type imageEditRequest struct {
	Rotate int  `json:"rotate"` // 顺时针旋转角度：0 / 90 / 180 / 270
	FlipH  bool `json:"flip_h"` // 旋转后水平翻转
	FlipV  bool `json:"flip_v"` // 旋转后垂直翻转
	Crop   *struct {
		X, Y, W, H int
	} `json:"crop"` // 在旋转、翻转后的图片上裁剪（像素）
}

// transformImage 按顺序执行：顺时针旋转 → 翻转 → 裁剪。
func transformImage(src image.Image, req imageEditRequest) (image.Image, bool) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	rot := ((req.Rotate % 360) + 360) % 360
	if rot%90 != 0 {
		return nil, false
	}
	ow, oh := w, h
	if rot == 90 || rot == 270 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch rot {
			case 90:
				nx, ny = h-1-y, x
			case 180:
				nx, ny = w-1-x, h-1-y
			case 270:
				nx, ny = y, w-1-x
			default:
				nx, ny = x, y
			}
			if req.FlipH {
				nx = ow - 1 - nx
			}
			if req.FlipV {
				ny = oh - 1 - ny
			}
			out.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	if c := req.Crop; c != nil {
		r := image.Rect(c.X, c.Y, c.X+c.W, c.Y+c.H).Intersect(out.Bounds())
		if r.Dx() < 1 || r.Dy() < 1 {
			return nil, false
		}
		cropped := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(cropped, cropped.Bounds(), out, r.Min, draw.Src)
		return cropped, true
	}
	return out, true
}

// EditMyFile POST /users/me/files/:id/edit 编辑自己的图片并另存为新文件，返回 {file, references}。
func (a *App) EditMyFile(c *gin.Context) {
	u := currentUser(c)
	var req imageEditRequest
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var f models.UserFile
	if a.DB.Where("id = ? AND user_id = ?", c.Param("id"), u.ID).First(&f).Error != nil {
		fail(c, http.StatusNotFound, "文件不存在")
		return
	}
	ext := strings.ToLower(f.Ext)
	if !editableImageExts[ext] {
		fail(c, http.StatusBadRequest, "该格式暂不支持编辑，支持 PNG、JPEG、GIF")
		return
	}
	data, err := storage.ReadFile(config.DataDir(), f.Driver, f.Name, f.URL)
	if err != nil {
		fail(c, http.StatusBadGateway, "读取原图失败："+err.Error())
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		fail(c, http.StatusBadRequest, "无法识别的图片")
		return
	}
	if cfg.Width*cfg.Height > maxEditPixels {
		fail(c, http.StatusBadRequest, "图片过大，暂不支持编辑")
		return
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		fail(c, http.StatusBadRequest, "无法识别的图片")
		return
	}
	out, valid := transformImage(src, req)
	if !valid {
		fail(c, http.StatusBadRequest, "旋转角度或裁剪范围无效")
		return
	}
	var buf bytes.Buffer
	outExt := ".png"
	if ext == "jpg" || ext == "jpeg" {
		outExt = "." + ext
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: 92})
	} else {
		err = png.Encode(&buf, out)
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "保存图片失败")
		return
	}
	url, err := a.storeUserFile(u, "edit", outExt, buf.Bytes())
	if isStorageFull(err) {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var created models.UserFile
	a.DB.Where("user_id = ? AND url = ?", u.ID, url).Order("id DESC").First(&created)
	ok(c, gin.H{"file": created, "references": 0})
}
