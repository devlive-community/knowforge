package contentcollect

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 仅供外部测试包（contentcollect_test）使用的内部入口。

type WebPage = webPage

// SetWebFetchers 替换静态抓取 / 浏览器渲染实现（nil 表示不替换），测试结束自动还原。
func SetWebFetchers(t testing.TB, fetch, render func(context.Context, *url.URL) (WebPage, error)) {
	oldFetch, oldRender := webFetcher, webRenderer
	webFetcher, webRenderer = fetch, render
	t.Cleanup(func() { webFetcher, webRenderer = oldFetch, oldRender })
}

func CreateImportedWebDocument(core plugincore.Core, book *models.Book, u *models.User, title, content string, parentID *uint) (models.Document, error) {
	return (&behavior{core: core}).createImportedWebDocument(book, u, title, content, parentID, nil)
}

func RunSiteCrawlJob(core plugincore.Core, jobID uint) error {
	raw, _ := json.Marshal(siteCrawlJobPayload{JobID: jobID})
	return (&behavior{core: core}).runSiteCrawlJob(context.Background(), raw)
}

// SetCrawlPageTimeout 缩短整站采集的单页超时，测试结束自动还原。
func SetCrawlPageTimeout(t testing.TB, d time.Duration) {
	old := crawlPageTimeoutFor
	crawlPageTimeoutFor = func() time.Duration { return d }
	t.Cleanup(func() { crawlPageTimeoutFor = old })
}
