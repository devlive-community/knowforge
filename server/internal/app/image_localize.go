package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/safehttp"
)

// 外链图片本地化（Issue #87）：把书中章节引用的外部图片下载后存入当前存储驱动（本地 / 七牛 / S3 兼容），并改写引用，
// 避免外链失效或防盗链。下载经 safehttp（拒绝内网地址与越权重定向、限制大小），作为后台任务执行，改动的章节生成版本记录便于回滚。

const (
	imageLocalizeJobType   = "content.images.localize"
	imageLocalizeMaxImages = 500
	imageLocalizeMaxErrors = 20
)

// localizeHTTPClient 下载外链图片的客户端（测试可替换以访问本地模拟服务）。
var localizeHTTPClient = func(ctx context.Context, limit int64) *http.Client { return safehttp.NewClient(ctx, limit) }

type imageLocalizeJob struct {
	UserID uint `json:"user_id"`
	BookID uint `json:"book_id"`
}

type imageLocalizeFailure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type imageLocalizeResult struct {
	Localized    int                    `json:"localized"`
	Failed       int                    `json:"failed"`
	DocsChanged  int                    `json:"docs_changed"`
	Failures     []imageLocalizeFailure `json:"failures"`
	LimitReached bool                   `json:"limit_reached"`
}

// imageContentExt 按响应类型确定图片扩展名（不认识的类型不保存）。
var imageContentExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg", "image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico",
}

// ownImageHosts 已属于本站的图片主机（站点地址与各存储驱动的访问域名），这些链接不再下载。
func (a *App) ownImageHosts() map[string]bool {
	hosts := map[string]bool{}
	for _, key := range []string{"site_url", "qiniu_domain", "s3_public_url", "s3_endpoint"} {
		if u, err := url.Parse(strings.TrimSpace(a.getSetting(key))); err == nil && u.Host != "" {
			hosts[strings.ToLower(u.Host)] = true
		}
	}
	if bucket, endpoint := a.getSetting("s3_bucket"), a.getSetting("s3_endpoint"); bucket != "" {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			hosts[strings.ToLower(bucket+"."+u.Host)] = true
		}
	}
	return hosts
}

// externalImageURL 返回需要本地化的外部图片地址（仅 http(s) 且不属于本站）。
func externalImageURL(ref string, own map[string]bool) (string, bool) {
	ref = strings.TrimSpace(ref)
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || own[strings.ToLower(u.Host)] {
		return "", false
	}
	return ref, true
}

// localizeBookImages 本地化一本书所有章节中的外链图片。
func (a *App) localizeBookImages(ctx context.Context, book *models.Book, u *models.User) (imageLocalizeResult, error) {
	res := imageLocalizeResult{Failures: []imageLocalizeFailure{}}
	var docs []models.Document
	if err := a.DB.Where("book_id = ?", book.ID).Order("id").Find(&docs).Error; err != nil {
		return res, err
	}
	own := a.ownImageHosts()
	allowed := a.uploadAllowedExts()
	maxBytes := a.userUploadMaxBytes(u)
	client := localizeHTTPClient(ctx, maxBytes+1)
	done := map[string]string{}   // 外链 → 新地址
	failed := map[string]string{} // 外链 → 失败原因（同一地址不重复尝试）
	attempts := 0

	download := func(raw string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "KnowForge-ImageLocalizer/1.0")
		req.Header.Set("Accept", "image/*")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("返回状态码 %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if int64(len(data)) > maxBytes {
			return "", fmt.Errorf("图片超过 %d MB", maxBytes>>20)
		}
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
		ext, known := imageContentExt[contentType]
		if !known {
			ext, known = imageContentExt[strings.Split(http.DetectContentType(data), ";")[0]]
		}
		if !known {
			return "", fmt.Errorf("不是图片（%s）", contentType)
		}
		if !allowed[ext] {
			return "", fmt.Errorf("站点不允许上传 %s 图片", ext)
		}
		return a.storeUserFile(u, "image_localize", ext, data)
	}

	for i := range docs {
		doc := &docs[i]
		changed := false
		replace := func(match, ref string) string {
			raw, external := externalImageURL(ref, own)
			if !external {
				return match
			}
			if newURL, ok := done[raw]; ok {
				changed = true
				return strings.Replace(match, ref, newURL, 1)
			}
			if _, bad := failed[raw]; bad {
				return match
			}
			if attempts >= imageLocalizeMaxImages {
				res.LimitReached = true
				return match
			}
			attempts++
			newURL, err := download(raw)
			if err != nil {
				failed[raw] = err.Error()
				res.Failed++
				if len(res.Failures) < imageLocalizeMaxErrors {
					res.Failures = append(res.Failures, imageLocalizeFailure{URL: raw, Error: err.Error()})
				}
				return match
			}
			done[raw] = newURL
			res.Localized++
			changed = true
			return strings.Replace(match, ref, newURL, 1)
		}
		content := mdImageRefPattern.ReplaceAllStringFunc(doc.Content, func(m string) string { return replace(m, mdImageRefPattern.FindStringSubmatch(m)[1]) })
		content = mdHTMLImgPattern.ReplaceAllStringFunc(content, func(m string) string { return replace(m, mdHTMLImgPattern.FindStringSubmatch(m)[1]) })
		if !changed || content == doc.Content {
			continue
		}
		oldDoc := *doc
		doc.Content = content
		if err := a.DB.Model(&models.Document{}).Where("id = ?", doc.ID).Updates(map[string]any{"content": content, "updated_at": time.Now()}).Error; err != nil {
			return res, err
		}
		a.notifyDocumentIndexableChange(book, book, &oldDoc, doc)
		revision := newDocumentRevision(doc, u.ID, "save")
		_ = a.DB.Create(&revision).Error
		res.DocsChanged++
	}
	return res, nil
}

func (a *App) runImageLocalizeJob(ctx context.Context, raw json.RawMessage) (any, error) {
	var job imageLocalizeJob
	if err := json.Unmarshal(raw, &job); err != nil || job.UserID == 0 || job.BookID == 0 {
		return nil, fmt.Errorf("外链图片本地化任务参数无效")
	}
	var user models.User
	var book models.Book
	if a.DB.First(&user, job.UserID).Error != nil || a.DB.First(&book, job.BookID).Error != nil {
		return nil, fmt.Errorf("书籍或用户不存在")
	}
	return a.localizeBookImages(ctx, &book, &user)
}

// LocalizeBookImages POST /books/:id/cleanup/localize-images 本地化书中外链图片（有任务队列时返回 202 与 task）。
func (a *App) LocalizeBookImages(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "无权操作该书籍")
		return
	}
	a.recordAudit(c, "book.images_localized", "book", auditID(book.ID), book.Title, changedFields("content"))
	if queue := a.jobQueue(); queue != nil {
		job, err := queue.EnqueueOwned(c.Request.Context(), u.ID, imageLocalizeJobType, imageLocalizeJob{UserID: u.ID, BookID: book.ID}, 1)
		if err != nil {
			fail(c, http.StatusInternalServerError, "创建任务失败")
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task": publicBackgroundJob(job), "message": "已开始在后台本地化外链图片"}})
		return
	}
	res, err := a.localizeBookImages(c.Request.Context(), book, u)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, res)
}
