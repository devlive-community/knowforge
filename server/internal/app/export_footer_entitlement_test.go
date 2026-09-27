package app

import (
	"testing"

	"knowforge/server/internal/models"
)

// 导出页脚：书籍页脚需书籍作者有「自定义导出页脚」权益，个人页脚需导出者有该权益，否则回退到「Powered by 站点名」。
func TestExportFooterEntitlement(t *testing.T) {
	app, owner, db := newContentImportTestApp(t)
	owner.Role = "user"
	db.Save(owner)
	reader := &models.User{Username: "footer-reader", Email: "footer-reader@test.local", Role: "user", IsActive: true}
	db.Create(reader)
	book := &models.Book{Title: "页脚书", Slug: "footer-book", UserID: owner.ID}
	db.Create(book)
	db.Create(&models.UserExportSetting{UserID: reader.ID, Footer: "读者的页脚"})
	_ = app.setSetting("site_name", "测试站", "")

	if got := app.resolveExportFooter(book, reader); got != "读者的页脚" {
		t.Fatalf("有权益时应使用个人页脚: %q", got)
	}
	db.Create(&models.BookExportSetting{BookID: book.ID, Footer: "作者的页脚"})
	if got := app.resolveExportFooter(book, reader); got != "作者的页脚" {
		t.Fatalf("书籍页脚优先: %q", got)
	}

	_ = app.setSetting(cfgCustomFooter, "0", "")
	if got := app.resolveExportFooter(book, reader); got != "Powered by 测试站" {
		t.Fatalf("没有权益时应使用站点页脚: %q", got)
	}
	admin := &models.User{Username: "footer-admin", Email: "footer-admin@test.local", Role: "admin", IsActive: true}
	db.Create(admin)
	book.UserID = admin.ID
	db.Save(book)
	if got := app.resolveExportFooter(book, reader); got != "作者的页脚" {
		t.Fatalf("书籍作者是管理员时书籍页脚仍生效: %q", got)
	}
}
