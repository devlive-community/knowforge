package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"knowforge/server/internal/config"

	"github.com/gin-gonic/gin"
)

// allowedImageExts 内置默认允许的图片类型（管理员未自定义时使用）
var allowedImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".ico": true,
}

// Upload POST /upload 上传图片到本地数据目录
func (a *App) Upload(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, "请选择要上传的文件")
		return
	}
	defer file.Close()

	// 上传大小按用户权益（基础值即「内容设置」的上传大小，等级/会员可放宽）
	if limit := a.userUploadMaxBytes(currentUser(c)); header.Size > limit {
		fail(c, http.StatusBadRequest, fmt.Sprintf("文件不能超过 %d MB", limit>>20))
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !a.uploadAllowedExts()[ext] {
		fail(c, http.StatusBadRequest, "不支持的文件类型："+ext)
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		fail(c, http.StatusBadRequest, "读取文件失败")
		return
	}
	if ext == ".svg" {
		if err := checkSVG(data); err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	url, err := a.storeUserFile(currentUser(c), "upload", ext, data)
	if isStorageFull(err) {
		fail(c, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"url": url})
}

// uploadName 随机文件名（日期-随机串+扩展名）。上传接口与导入（如 Markdown 包内图片）经 storeUserFile 写入管理员配置的存储。
func uploadName(ext string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成文件名失败")
	}
	return time.Now().Format("20060102") + "-" + hex.EncodeToString(buf)[:8] + ext, nil
}

// ServeUploads 将数据目录中的上传文件挂载到 /uploads
func (a *App) ServeUploads(r *gin.Engine) {
	dir := filepath.Join(config.DataDir(), "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Println("创建上传目录失败:", err)
		return
	}
	uploads := r.Group("/uploads")
	uploads.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		c.Next()
	})
	uploads.StaticFS("/", http.Dir(dir))
}

// M22 存储驱动配置（local | qiniu），凭据存站点配置表，不出现在公开 /site

var storageDrivers = map[string]bool{"local": true, "qiniu": true, "s3": true}

type storageConfigUpdate struct {
	Driver          *string `json:"driver"`
	QiniuAccessKey  *string `json:"qiniu_access_key"`
	QiniuSecretKey  *string `json:"qiniu_secret_key"`
	QiniuBucket     *string `json:"qiniu_bucket"`
	QiniuDomain     *string `json:"qiniu_domain"`
	QiniuUploadHost *string `json:"qiniu_upload_host"`
	// S3 兼容对象存储（AWS S3 / 阿里云 OSS / 腾讯云 COS / MinIO / R2）；secret 只写，空串表示不修改
	S3Endpoint  *string `json:"s3_endpoint"`
	S3Region    *string `json:"s3_region"`
	S3Bucket    *string `json:"s3_bucket"`
	S3AccessKey *string `json:"s3_access_key"`
	S3SecretKey *string `json:"s3_secret_key"`
	S3PublicURL *string `json:"s3_public_url"`
	S3PathStyle *bool   `json:"s3_path_style"`
	S3Prefix    *string `json:"s3_prefix"`
}

// AdminGetStorage GET /storage 管理员读取存储配置
func (a *App) AdminGetStorage(c *gin.Context) {
	ok(c, gin.H{
		"driver":            a.getSetting("storage_driver"),
		"qiniu_access_key":  a.getSetting("qiniu_access_key"),
		"qiniu_secret_key":  a.getSetting("qiniu_secret_key"),
		"qiniu_bucket":      a.getSetting("qiniu_bucket"),
		"qiniu_domain":      a.getSetting("qiniu_domain"),
		"qiniu_upload_host": a.getSetting("qiniu_upload_host"),
		"s3_endpoint":       a.getSetting("s3_endpoint"),
		"s3_region":         a.getSetting("s3_region"),
		"s3_bucket":         a.getSetting("s3_bucket"),
		"s3_access_key":     a.getSetting("s3_access_key"),
		"s3_secret_key_set": a.getSetting("s3_secret_key") != "",
		"s3_public_url":     a.getSetting("s3_public_url"),
		"s3_path_style":     a.getSetting("s3_path_style") == "true",
		"s3_prefix":         a.getSetting("s3_prefix"),
	})
}

// AdminSaveStorage PUT /storage 管理员保存存储配置
func (a *App) AdminSaveStorage(c *gin.Context) {
	var req storageConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fields := []string{}
	if req.Driver != nil {
		fields = append(fields, "driver")
		driver := *req.Driver
		if !storageDrivers[driver] {
			fail(c, http.StatusBadRequest, "存储驱动必须为 local、qiniu 或 s3")
			return
		}
		if err := a.setSetting("storage_driver", driver, "上传存储驱动 local|qiniu"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	for _, item := range []struct {
		key   string
		value *string
		desc  string
	}{
		{"qiniu_access_key", req.QiniuAccessKey, "七牛 Access Key"},
		{"qiniu_secret_key", req.QiniuSecretKey, "七牛 Secret Key"},
		{"qiniu_bucket", req.QiniuBucket, "七牛存储空间名"},
		{"qiniu_domain", req.QiniuDomain, "七牛 CDN 绑定域名（含 https://）"},
		{"qiniu_upload_host", req.QiniuUploadHost, "七牛上传区域地址"},
		{"s3_endpoint", req.S3Endpoint, "S3 兼容存储服务地址（含 https://）"},
		{"s3_region", req.S3Region, "S3 兼容存储区域"},
		{"s3_bucket", req.S3Bucket, "S3 兼容存储桶"},
		{"s3_access_key", req.S3AccessKey, "S3 兼容存储 Access Key"},
		{"s3_secret_key", req.S3SecretKey, "S3 兼容存储 Secret Key"},
		{"s3_public_url", req.S3PublicURL, "S3 兼容存储对外访问地址（CDN，含 https://）"},
		{"s3_prefix", req.S3Prefix, "S3 兼容存储对象键前缀"},
	} {
		if item.value == nil || (item.key == "s3_secret_key" && strings.TrimSpace(*item.value) == "") {
			continue
		}
		fields = append(fields, item.key)
		if (item.key == "qiniu_domain" || item.key == "qiniu_upload_host" || item.key == "s3_endpoint" || item.key == "s3_public_url") && strings.TrimSpace(*item.value) != "" &&
			!strings.HasPrefix(*item.value, "http://") && !strings.HasPrefix(*item.value, "https://") {
			fail(c, http.StatusBadRequest, item.desc+"必须以 http(s):// 开头")
			return
		}
		if err := a.setSetting(item.key, strings.TrimSpace(*item.value), item.desc); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	if req.S3PathStyle != nil {
		fields = append(fields, "s3_path_style")
		if err := a.setSetting("s3_path_style", strconv.FormatBool(*req.S3PathStyle), "S3 兼容存储使用路径风格（MinIO 等）"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
	}
	a.recordAudit(c, "storage.updated", "config", "storage", "存储配置", map[string]any{"changed_fields": fields})
	ok(c, gin.H{"message": "已保存"})
}
