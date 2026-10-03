package storage

import (
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

// —— 存储迁移所需：判断文件是否已在当前存储、从原存储读回文件内容、列出本地上传目录。——

// PublicBase 当前驱动保存的文件地址前缀（local 为 /uploads/，七牛为 CDN 域名，S3 为对外地址或对象地址前缀）；
// 地址以此开头的文件即已在当前存储中。
func PublicBase(u Uploader) string {
	switch up := u.(type) {
	case *QiniuUploader:
		return strings.TrimRight(up.cfg.QiniuDomain, "/") + "/"
	case *S3Uploader:
		prefix := strings.Trim(up.cfg.S3Prefix, "/")
		if prefix != "" {
			prefix += "/"
		}
		if base := strings.TrimRight(up.cfg.S3PublicURL, "/"); base != "" {
			return base + "/" + s3EscapePath(prefix)
		}
		if objectURL, err := s3ObjectURL(up.cfg, prefix); err == nil {
			return objectURL.String()
		}
		return ""
	default:
		return "/uploads/"
	}
}

// Current 当前站点配置的上传驱动（凭据不完整时为 local）。
func Current(db *gorm.DB, dataDir string) Uploader { return FromSettings(db, dataDir) }

var downloadClient = &http.Client{Timeout: 2 * time.Minute}

// ReadFile 读回已上传的文件：本地文件直接读取，云存储按公开地址下载。
func ReadFile(dataDir, driver, name, fileURL string) ([]byte, error) {
	if driver == "local" || strings.HasPrefix(fileURL, "/uploads/") {
		if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, errors.New("文件名无效")
		}
		return os.ReadFile(filepath.Join(dataDir, "uploads", name))
	}
	if !strings.HasPrefix(fileURL, "http://") && !strings.HasPrefix(fileURL, "https://") {
		return nil, fmt.Errorf("无法读取文件地址 %s", fileURL)
	}
	resp, err := downloadClient.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("下载原文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载原文件失败 (%d)", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<30))
}

// LocalFiles 本地上传目录中的文件名（不含子目录）。
func LocalFiles(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dataDir, "uploads"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
