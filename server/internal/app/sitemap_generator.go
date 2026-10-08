package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
)

// sitemap 后台生成：直连数据库构建静态 sitemap 文件（index + 分片），
// 写入与 web 端约定的 SITEMAP_DIR 目录，web 路由只负责读取文件返回。
// 站点地址来自站点设置 site_url（管理员在站点设置页配置），未配置时跳过生成。

const (
	sitemapJobType    = "sitemap.generate"
	sitemapInterval   = 24 * time.Hour
	booksPerShard     = 100
	maxURLsPerShard   = 20000
	sitemapIndexFile  = "sitemap.xml"
	sitemapDirEnv     = "SITEMAP_DIR"
	sitemapShardRegex = `^(\d+)\.xml$`
)

// sitemapDir 返回 sitemap 静态文件目录：优先 SITEMAP_DIR 环境变量，
// 否则探测常见仓库布局下的 web 缓存目录，最后回退数据目录。
func sitemapDir() string {
	if dir := os.Getenv(sitemapDirEnv); dir != "" {
		return dir
	}
	for _, candidate := range []string{
		"../app/web/.sitemap-cache", // server 工作目录在 server/ 时
		"app/web/.sitemap-cache",    // 工作目录在仓库根时
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join(config.DataDir(), "sitemaps")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// runSitemapGenerate 后台任务：全量重建 sitemap index 与分片。
// 可见性与游客一致：公开书籍（is_public + 状态可读 + 非仅登录可读）及其已发布章节。
func (a *App) runSitemapGenerate(ctx context.Context, _ json.RawMessage) error {
	requestRevision := a.getSetting("sitemap_request_revision")
	siteURL := strings.TrimRight(strings.TrimSpace(a.getSetting("site_url")), "/")
	if siteURL == "" {
		// 未配置站点地址时没有可用 sitemap；把当前请求标记为已处理，避免后台任务循环重排。
		// 管理员稍后保存 site_url 时会创建新的请求标记并立即重建。
		if requestRevision != "" {
			if err := a.setSetting("sitemap_built_revision", requestRevision, "最近完成的 Sitemap 刷新标记"); err != nil {
				return fmt.Errorf("保存 sitemap 刷新状态失败: %w", err)
			}
		}
		return nil
	}

	dir := sitemapDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 sitemap 目录失败: %w", err)
	}

	// 分页拉取公开书籍（与游客在 /books 看到的范围一致），按 id 排序保证分片稳定
	shards := 0
	var bookBatch []models.Book
	lastID := uint(0)
	for {
		if err := a.DB.WithContext(ctx).
			Where("is_public = ? AND status IN ? AND login_required = ? AND id > ?", true, publiclyReadableBookStatuses, false, lastID).
			Order("id ASC").Limit(booksPerShard).Find(&bookBatch).Error; err != nil {
			return fmt.Errorf("查询公开书籍失败: %w", err)
		}
		if len(bookBatch) == 0 {
			break
		}
		xml, err := a.buildSitemapShardXML(ctx, bookBatch, siteURL, shards == 0)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.xml", shards)), []byte(xml), 0o644); err != nil {
			return fmt.Errorf("写入 sitemap 分片失败: %w", err)
		}
		lastID = bookBatch[len(bookBatch)-1].ID
		shards++
		if len(bookBatch) < booksPerShard {
			break
		}
	}
	if shards == 0 {
		// 没有公开书籍时也输出仅含静态页的第 0 片，保证 sitemap 可访问
		xml, err := a.buildSitemapShardXML(ctx, nil, siteURL, true)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "0.xml"), []byte(xml), 0o644); err != nil {
			return fmt.Errorf("写入 sitemap 分片失败: %w", err)
		}
		shards = 1
	}

	// 清理超出分片数的旧文件（书籍减少导致分片数下降时）
	if entries, err := os.ReadDir(dir); err == nil {
		re := regexp.MustCompile(sitemapShardRegex)
		for _, e := range entries {
			if m := re.FindStringSubmatch(e.Name()); m != nil {
				var n int
				if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil && n >= shards {
					_ = os.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}

	// 生成 index
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<sitemapindex xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	today := time.Now().UTC().Format("2006-01-02")
	for n := 0; n < shards; n++ {
		fmt.Fprintf(&sb, "  <sitemap>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n  </sitemap>\n",
			xmlEscape(fmt.Sprintf("%s/sitemaps/%d.xml", siteURL, n)), today)
	}
	sb.WriteString("</sitemapindex>\n")
	if err := os.WriteFile(filepath.Join(dir, sitemapIndexFile), []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("写入 sitemap index 失败: %w", err)
	}
	if requestRevision != "" {
		if err := a.setSetting("sitemap_built_revision", requestRevision, "最近完成的 Sitemap 刷新标记"); err != nil {
			return fmt.Errorf("保存 sitemap 刷新状态失败: %w", err)
		}
	}
	return nil
}

// buildSitemapShardXML 生成单个分片：一批书籍的详情页 + 各书已发布章节阅读页。
// includeStatic 控制是否附带静态页（仅第 0 片附带）。
func (a *App) buildSitemapShardXML(ctx context.Context, books []models.Book, siteURL string, includeStatic bool) (string, error) {
	type entry struct {
		loc     string
		lastmod string
	}
	entries := []entry{}

	if includeStatic {
		for _, p := range []string{"", "/explore", "/login", "/register"} {
			entries = append(entries, entry{loc: siteURL + p})
		}
	}

	// 一次查出本批书籍的全部已发布章节
	bookIDs := make([]uint, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	docsByBook := map[uint][]models.Document{}
	if len(bookIDs) > 0 {
		var docs []models.Document
		if err := a.DB.WithContext(ctx).
			Select("id", "book_id", "parent_id", "title", "slug", "status", "created_at", "updated_at").
			Where("book_id IN ? AND status = ?", bookIDs, "published").
			Order("sort_order ASC, created_at ASC").Find(&docs).Error; err != nil {
			return "", fmt.Errorf("查询公开章节失败: %w", err)
		}
		for i := range docs {
			docsByBook[docs[i].BookID] = append(docsByBook[docs[i].BookID], docs[i])
		}
	}

	// 按 (book, sort_order, created_at) 顺序输出，与站点目录的阅读顺序一致
	for _, b := range books {
		if len(entries) >= maxURLsPerShard {
			break
		}
		entries = append(entries, entry{
			loc:     fmt.Sprintf("%s/book/detail/%s", siteURL, xmlEscape(b.Slug)),
			lastmod: lastmodOf(b.UpdatedAt, b.CreatedAt),
		})
		docs := docsByBook[b.ID]
		sort.SliceStable(docs, func(i, j int) bool {
			if docs[i].SortOrder != docs[j].SortOrder {
				return docs[i].SortOrder < docs[j].SortOrder
			}
			return docs[i].CreatedAt.Before(docs[j].CreatedAt)
		})
		for _, d := range docs {
			if len(entries) >= maxURLsPerShard {
				break
			}
			if d.Slug == "" {
				continue
			}
			entries = append(entries, entry{
				loc:     fmt.Sprintf("%s/book/reader/%s/%s", siteURL, xmlEscape(b.Slug), xmlEscape(d.Slug)),
				lastmod: lastmodOf(d.UpdatedAt, d.CreatedAt),
			})
		}
	}

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	for _, e := range entries {
		if e.lastmod != "" {
			fmt.Fprintf(&sb, "  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n  </url>\n", xmlEscape(e.loc), e.lastmod)
		} else {
			fmt.Fprintf(&sb, "  <url>\n    <loc>%s</loc>\n  </url>\n", xmlEscape(e.loc))
		}
	}
	sb.WriteString("</urlset>\n")
	return sb.String(), nil
}

func lastmodOf(updated, created time.Time) string {
	if !updated.IsZero() {
		return updated.UTC().Format("2006-01-02")
	}
	if !created.IsZero() {
		return created.UTC().Format("2006-01-02")
	}
	return ""
}

// enqueueSitemapGenerate 请求尽快重建 sitemap（站点地址或公开内容变化后调用）。
func (a *App) enqueueSitemapGenerate() { a.enqueueSitemapGenerateSoon() }
