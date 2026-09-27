package storage

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Deleter 可删除已上传文件的驱动（name 为 Upload 时的文件名）。
type Deleter interface {
	Delete(name string) error
}

// DriverName 驱动名称（local | qiniu | s3），用于记录文件由哪个驱动保存。
func DriverName(u Uploader) string {
	switch u.(type) {
	case *S3Uploader:
		return "s3"
	case *QiniuUploader:
		return "qiniu"
	default:
		return "local"
	}
}

// ForDriver 按站点配置构造指定驱动（凭据不完整时返回 nil）；用于删除当初由该驱动保存的文件，即使站点之后切换了驱动。
func ForDriver(db *gorm.DB, dataDir, driver string) Uploader {
	cfg := loadConfig(db)
	cfg.Driver = driver
	u := FromConfig(cfg, dataDir)
	if DriverName(u) != driver {
		return nil
	}
	return u
}

func (u *LocalUploader) Delete(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New("文件名无效")
	}
	err := os.Remove(filepath.Join(u.dataDir, "uploads", name))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除文件失败: %w", err)
	}
	return nil
}

func (u *S3Uploader) Delete(name string) error {
	key := strings.TrimLeft(strings.Trim(u.cfg.S3Prefix, "/")+"/"+name, "/")
	objectURL, err := s3ObjectURL(u.cfg, key)
	if err != nil {
		return err
	}
	now := time.Now
	if u.now != nil {
		now = u.now
	}
	req, err := http.NewRequest(http.MethodDelete, objectURL.String(), nil)
	if err != nil {
		return fmt.Errorf("构造删除请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	signS3Request(req, nil, u.cfg, now().UTC())
	resp, err := s3Client.Do(req)
	if err != nil {
		return fmt.Errorf("从对象存储删除失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("从对象存储删除失败 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// qiniuRSHost 七牛资源管理接口地址（可在测试中替换）。
var qiniuRSHost = "https://rs.qiniuapi.com"

func (u *QiniuUploader) Delete(name string) error {
	entry := base64.URLEncoding.EncodeToString([]byte(u.cfg.QiniuBucket + ":" + name))
	path := "/delete/" + entry
	mac := hmac.New(sha1.New, []byte(u.cfg.QiniuSecretKey))
	mac.Write([]byte(path + "\n"))
	req, err := http.NewRequest(http.MethodPost, qiniuRSHost+path, nil)
	if err != nil {
		return fmt.Errorf("构造删除请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "QBox "+u.cfg.QiniuAccessKey+":"+urlsafeBase64(mac.Sum(nil)))
	resp, err := s3Client.Do(req)
	if err != nil {
		return fmt.Errorf("从七牛删除失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == 612 { // 612：文件不存在
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("从七牛删除失败 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
