package app

import (
	"net/http"
	"strings"

	"knowforge/server/internal/models"

	"github.com/gin-gonic/gin"
)

var exportPageSizes = map[string]bool{"A4": true, "Letter": true}
var exportMargins = map[string]bool{"narrow": true, "normal": true, "wide": true}
var exportCodeThemes = map[string]bool{"light": true, "dark": true}

const maxExportFooterLength = 100

// normalizeExportFooter 去除换行并按长度截断，避免破坏 PDF 页脚模板
func normalizeExportFooter(footer string) string {
	footer = strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(footer))
	if runes := []rune(footer); len(runes) > maxExportFooterLength {
		footer = string(runes[:maxExportFooterLength])
	}
	return footer
}

// defaultExportSetting 未配置时的默认导出样式
func defaultExportSetting(userID uint) models.UserExportSetting {
	return models.UserExportSetting{
		UserID: userID, PageSize: "A4", IncludeCover: true, IncludeToc: true,
		FontSize: 15, CodeTheme: "light", Margin: "normal",
	}
}

// GetExportSettings GET /me/export-settings 当前用户的导出样式偏好
func (a *App) GetExportSettings(c *gin.Context) {
	u := currentUser(c)
	var s models.UserExportSetting
	if err := a.DB.Where("user_id = ?", u.ID).First(&s).Error; err != nil {
		ok(c, defaultExportSetting(u.ID))
		return
	}
	ok(c, s)
}

// UpdateExportSettings PUT /me/export-settings 保存导出样式偏好
func (a *App) UpdateExportSettings(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		PageSize     *string `json:"page_size"`
		IncludeCover *bool   `json:"include_cover"`
		IncludeToc   *bool   `json:"include_toc"`
		FontSize     *int    `json:"font_size"`
		CodeTheme    *string `json:"code_theme"`
		Margin       *string `json:"margin"`
		Footer       *string `json:"footer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}

	var s models.UserExportSetting
	if err := a.DB.Where("user_id = ?", u.ID).First(&s).Error; err != nil {
		s = defaultExportSetting(u.ID)
	}
	if req.PageSize != nil && exportPageSizes[*req.PageSize] {
		s.PageSize = *req.PageSize
	}
	if req.IncludeCover != nil {
		s.IncludeCover = *req.IncludeCover
	}
	if req.IncludeToc != nil {
		s.IncludeToc = *req.IncludeToc
	}
	if req.FontSize != nil && *req.FontSize >= 12 && *req.FontSize <= 20 {
		s.FontSize = *req.FontSize
	}
	if req.CodeTheme != nil && exportCodeThemes[*req.CodeTheme] {
		s.CodeTheme = *req.CodeTheme
	}
	if req.Margin != nil && exportMargins[*req.Margin] {
		s.Margin = *req.Margin
	}
	if req.Footer != nil {
		s.Footer = normalizeExportFooter(*req.Footer)
	}
	if err := a.DB.Save(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	ok(c, s)
}

// applyExportStyleFields 校验并写入通用导出样式字段（书籍与用户样式共用）
func applyExportStyleFields(pageSize, codeTheme, margin *string, fontSize *int, includeCover, includeToc *bool,
	setPage func(string), setCover func(bool), setToc func(bool), setFont func(int), setTheme func(string), setMargin func(string)) {
	if pageSize != nil && exportPageSizes[*pageSize] {
		setPage(*pageSize)
	}
	if includeCover != nil {
		setCover(*includeCover)
	}
	if includeToc != nil {
		setToc(*includeToc)
	}
	if fontSize != nil && *fontSize >= 12 && *fontSize <= 20 {
		setFont(*fontSize)
	}
	if codeTheme != nil && exportCodeThemes[*codeTheme] {
		setTheme(*codeTheme)
	}
	if margin != nil && exportMargins[*margin] {
		setMargin(*margin)
	}
}

type exportStyleReq struct {
	PageSize     *string `json:"page_size"`
	IncludeCover *bool   `json:"include_cover"`
	IncludeToc   *bool   `json:"include_toc"`
	FontSize     *int    `json:"font_size"`
	CodeTheme    *string `json:"code_theme"`
	Margin       *string `json:"margin"`
	Footer       *string `json:"footer"`
}

// GetBookExportStyle GET /books/:id/export-style 书籍自有导出样式（未配置返回默认）
func (a *App) GetBookExportStyle(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canManageBook(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权访问该书籍设置")
		return
	}
	var s models.BookExportSetting
	if err := a.DB.Where("book_id = ?", book.ID).First(&s).Error; err != nil {
		ok(c, models.BookExportSetting{BookID: book.ID, PageSize: "A4", IncludeCover: true, IncludeToc: true, FontSize: 15, CodeTheme: "light", Margin: "normal"})
		return
	}
	ok(c, s)
}

// UpdateBookExportStyle PUT /books/:id/export-style 保存书籍自有导出样式
func (a *App) UpdateBookExportStyle(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	if !a.canManageBook(currentUser(c), book) {
		fail(c, http.StatusForbidden, "无权修改该书籍设置")
		return
	}
	var req exportStyleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var s models.BookExportSetting
	if err := a.DB.Where("book_id = ?", book.ID).First(&s).Error; err != nil {
		s = models.BookExportSetting{BookID: book.ID, PageSize: "A4", IncludeCover: true, IncludeToc: true, FontSize: 15, CodeTheme: "light", Margin: "normal"}
	}
	applyExportStyleFields(req.PageSize, req.CodeTheme, req.Margin, req.FontSize, req.IncludeCover, req.IncludeToc,
		func(v string) { s.PageSize = v }, func(v bool) { s.IncludeCover = v }, func(v bool) { s.IncludeToc = v },
		func(v int) { s.FontSize = v }, func(v string) { s.CodeTheme = v }, func(v string) { s.Margin = v })
	if req.Footer != nil {
		s.Footer = normalizeExportFooter(*req.Footer)
	}
	if err := a.DB.Save(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	ok(c, s)
}

// resolveExportStyle 依据 style 选择生效的导出样式：
// author（且作者开启共享）用书籍作者样式，否则用请求者自己的样式（匿名用默认）。
func (a *App) resolveExportStyle(style string, book *models.Book, u *models.User) models.UserExportSetting {
	// 作者共享样式且请求作者样式：优先书籍自有导出样式，其次作者个人样式
	if style == "author" && book.ExportStyleShared {
		var bs models.BookExportSetting
		if err := a.DB.Where("book_id = ?", book.ID).First(&bs).Error; err == nil {
			return models.UserExportSetting{
				PageSize: bs.PageSize, IncludeCover: bs.IncludeCover, IncludeToc: bs.IncludeToc,
				FontSize: bs.FontSize, CodeTheme: bs.CodeTheme, Margin: bs.Margin,
			}
		}
		var us models.UserExportSetting
		if err := a.DB.Where("user_id = ?", book.UserID).First(&us).Error; err == nil {
			return us
		}
		return defaultExportSetting(book.UserID)
	}
	// 否则用请求者自己的样式（匿名用默认）
	if u == nil {
		return defaultExportSetting(0)
	}
	var s models.UserExportSetting
	if err := a.DB.Where("user_id = ?", u.ID).First(&s).Error; err != nil {
		return defaultExportSetting(u.ID)
	}
	return s
}

// resolveExportFooter 解析每页页脚文案（Powered by …）：
// 优先书籍自有配置，其次导出者个人配置，最后回退默认 "Powered by <站点名>"。
func (a *App) resolveExportFooter(book *models.Book, u *models.User) string {
	// 书籍页脚需书籍作者有「自定义导出页脚」权益，个人页脚需导出者有该权益
	var bs models.BookExportSetting
	if err := a.DB.Where("book_id = ?", book.ID).First(&bs).Error; err == nil && strings.TrimSpace(bs.Footer) != "" {
		var owner models.User
		if a.DB.First(&owner, book.UserID).Error == nil && a.entitlement(&owner, entCustomFooter) > 0 {
			return bs.Footer
		}
	}
	if u != nil && a.entitlement(u, entCustomFooter) > 0 {
		var us models.UserExportSetting
		if err := a.DB.Where("user_id = ?", u.ID).First(&us).Error; err == nil && strings.TrimSpace(us.Footer) != "" {
			return us.Footer
		}
	}
	site := strings.TrimSpace(a.getSetting("site_name"))
	if site == "" {
		site = "KnowForge"
	}
	return "Powered by " + site
}
