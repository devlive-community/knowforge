package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var reportReasons = map[string]bool{
	"spam": true, "harassment": true, "copyright": true,
	"illegal": true, "misleading": true, "other": true,
}
var reportTargetTypes = map[string]bool{"book": true, "document": true, "comment": true}

// validReportTarget 内置类型或插件登记的用户内容类型（如问答的提问、回答）。
func validReportTarget(kind string) bool {
	if reportTargetTypes[kind] {
		return true
	}
	_, found := plugincore.UserContentFor(kind)
	return found
}

// reportTargetTypeList 全部可举报类型（内置在前），供管理端筛选。
func reportTargetTypeList() []string {
	return append([]string{"book", "document", "comment"}, plugincore.UserContentKinds()...)
}

var errReportAlreadyProcessed = errors.New("report already processed")

type reportTarget struct {
	Type  string
	ID    uint
	Label string
}

type adminReportItem struct {
	ID               uint       `json:"id"`
	ReporterID       uint       `json:"reporter_id"`
	ReporterUsername string     `json:"reporter_username"`
	ReporterEmail    string     `json:"reporter_email"`
	TargetType       string     `json:"target_type"`
	TargetID         uint       `json:"target_id"`
	TargetLabel      string     `json:"target_label"`
	Reason           string     `json:"reason"`
	Description      string     `json:"description"`
	Status           string     `json:"status"`
	Resolution       string     `json:"resolution"`
	ResolutionNote   string     `json:"resolution_note"`
	HandlerUsername  string     `json:"handler_username"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// CreateContentReport POST /reports 举报当前用户有权看到的内容。
func (a *App) CreateContentReport(c *gin.Context) {
	u := currentUser(c)
	var req struct {
		TargetType  string `json:"target_type"`
		TargetID    uint   `json:"target_id"`
		Reason      string `json:"reason"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReportTarget(req.TargetType) || req.TargetID == 0 || !reportReasons[req.Reason] {
		fail(c, http.StatusBadRequest, "举报参数无效")
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if utf8.RuneCountInString(req.Description) > 1000 {
		fail(c, http.StatusBadRequest, "补充说明不能超过 1000 个字符")
		return
	}
	target := a.findReportableTarget(u, req.TargetType, req.TargetID)
	if target == nil {
		fail(c, http.StatusNotFound, "举报内容不存在")
		return
	}
	var existing models.ContentReport
	lookupErr := a.DB.Where("reporter_id = ? AND target_type = ? AND target_id = ? AND status = ?", u.ID, target.Type, target.ID, "pending").First(&existing).Error
	if lookupErr == nil {
		fail(c, http.StatusConflict, "你已举报过该内容，请等待管理员处理")
		return
	}
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		fail(c, http.StatusInternalServerError, "检查举报记录失败")
		return
	}
	report := models.ContentReport{
		ReporterID: u.ID, TargetType: target.Type, TargetID: target.ID, TargetLabel: target.Label,
		Reason: req.Reason, Description: req.Description, Status: "pending",
	}
	if err := a.DB.Create(&report).Error; err != nil {
		fail(c, http.StatusInternalServerError, "提交举报失败")
		return
	}
	ok(c, gin.H{"id": report.ID, "status": report.Status, "message": "举报已提交"})
}

func (a *App) findReportableTarget(u *models.User, kind string, id uint) *reportTarget {
	switch kind {
	case "book":
		var book models.Book
		if err := a.DB.First(&book, id).Error; err != nil || !a.canReadBook(u, &book) {
			return nil
		}
		return &reportTarget{Type: kind, ID: book.ID, Label: book.Title}
	case "document":
		var doc models.Document
		var book models.Book
		if err := a.DB.First(&doc, id).Error; err != nil || a.DB.First(&book, doc.BookID).Error != nil || !a.canReadDocument(u, &doc, &book) {
			return nil
		}
		return &reportTarget{Type: kind, ID: doc.ID, Label: doc.Title}
	case "comment":
		var comment models.Comment
		var doc models.Document
		var book models.Book
		if err := a.DB.First(&comment, id).Error; err != nil || comment.Status != "published" ||
			a.DB.First(&doc, comment.DocumentID).Error != nil || a.DB.First(&book, doc.BookID).Error != nil ||
			!a.canReadDocument(u, &doc, &book) {
			return nil
		}
		return &reportTarget{Type: kind, ID: comment.ID, Label: trimRunes(comment.Content, 80)}
	}
	if uc, found := plugincore.UserContentFor(kind); found && u != nil {
		if ref, visible := uc.Resolve(a, u, id); visible {
			return &reportTarget{Type: kind, ID: id, Label: trimRunes(ref.Label, 80)}
		}
	}
	return nil
}

// AdminListContentReports GET /admin/reports 管理员分页查看举报人及处理记录。
func (a *App) AdminListContentReports(c *gin.Context) {
	page, pageSize := paginate(c)
	query := a.DB.Table("content_reports").
		Joins("JOIN users reporter ON reporter.id = content_reports.reporter_id").
		Joins("LEFT JOIN users handler ON handler.id = content_reports.handler_id")
	if status := strings.TrimSpace(c.Query("status")); status == "pending" || status == "resolved" || status == "rejected" {
		query = query.Where("content_reports.status = ?", status)
	}
	if targetType := strings.TrimSpace(c.Query("target_type")); validReportTarget(targetType) {
		query = query.Where("content_reports.target_type = ?", targetType)
	}
	if reason := strings.TrimSpace(c.Query("reason")); reportReasons[reason] {
		query = query.Where("content_reports.reason = ?", reason)
	}
	if keyword := strings.TrimSpace(c.Query("q")); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("content_reports.target_label LIKE ? OR reporter.username LIKE ? OR reporter.email LIKE ?", like, like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询举报失败")
		return
	}
	items := []adminReportItem{}
	if err := query.Select(`content_reports.id, content_reports.reporter_id, reporter.username AS reporter_username,
		reporter.email AS reporter_email, content_reports.target_type, content_reports.target_id,
		content_reports.target_label, content_reports.reason, content_reports.description, content_reports.status,
		content_reports.resolution, content_reports.resolution_note, COALESCE(handler.username, '') AS handler_username,
		content_reports.resolved_at, content_reports.created_at, content_reports.updated_at`).
		Order("CASE WHEN content_reports.status = 'pending' THEN 0 ELSE 1 END, content_reports.created_at DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).Scan(&items).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询举报失败")
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize, "target_types": reportTargetTypeList()})
}

// AdminResolveContentReport PUT /admin/reports/:id 驳回举报或下架目标内容。
func (a *App) AdminResolveContentReport(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	var req struct {
		Resolution string `json:"resolution"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Resolution != "reject" && req.Resolution != "takedown") {
		fail(c, http.StatusBadRequest, "处理方式无效")
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(req.Note) > 1000 {
		fail(c, http.StatusBadRequest, "处理说明不能超过 1000 个字符")
		return
	}
	var report models.ContentReport
	if err := a.DB.First(&report, id).Error; err != nil {
		fail(c, http.StatusNotFound, "举报不存在")
		return
	}
	if report.Status != "pending" {
		fail(c, http.StatusConflict, "该举报已经处理")
		return
	}
	now := currentTime()
	admin := currentUser(c)
	finalStatus := "rejected"
	if req.Resolution == "takedown" {
		finalStatus = "resolved"
	}
	var indexableURLs []string
	if req.Resolution == "takedown" {
		switch report.TargetType {
		case "book":
			var book models.Book
			if a.DB.First(&book, report.TargetID).Error == nil {
				indexableURLs = a.indexableBookURLs(&book)
			}
		case "document":
			var doc models.Document
			if a.DB.First(&doc, report.TargetID).Error == nil {
				var book models.Book
				if a.DB.First(&book, doc.BookID).Error == nil {
					if address := a.indexableDocumentURL(&book, &doc); address != "" {
						indexableURLs = append(indexableURLs, address)
					}
				}
			}
		}
	}
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		claimed := tx.Model(&models.ContentReport{}).Where("id = ? AND status = ?", report.ID, "pending").Updates(map[string]any{
			"status": finalStatus, "resolution": req.Resolution, "resolution_note": req.Note,
			"handler_id": admin.ID, "resolved_at": now,
		})
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			return errReportAlreadyProcessed
		}
		if req.Resolution == "takedown" && reportTargetTypes[report.TargetType] {
			if err := takedownReportedTarget(tx, &report); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, errReportAlreadyProcessed) {
			fail(c, http.StatusConflict, "该举报已经处理")
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, "被举报内容不存在")
		} else {
			fail(c, http.StatusInternalServerError, "处理举报失败")
		}
		return
	}
	// 插件登记的内容在事务之外下架（插件经自己的连接写库，SQLite 单写者下不能嵌套在事务内）；失败则撤回处理结果
	if req.Resolution == "takedown" && len(indexableURLs) > 0 {
		a.emitIndexableURLs(indexableURLs...)
	}
	if req.Resolution == "takedown" && !reportTargetTypes[report.TargetType] {
		err := errors.New("未登记的举报类型")
		if uc, found := plugincore.UserContentFor(report.TargetType); found {
			err = uc.SetVisible(a, report.TargetID, false)
		}
		if err != nil {
			a.DB.Model(&models.ContentReport{}).Where("id = ?", report.ID).Updates(map[string]any{
				"status": "pending", "resolution": "", "resolution_note": "", "handler_id": 0, "resolved_at": nil,
			})
			fail(c, http.StatusInternalServerError, "下架失败："+err.Error())
			return
		}
	}
	report.Status = finalStatus
	report.Resolution = req.Resolution
	resultText := "举报未通过审核"
	if req.Resolution == "takedown" {
		resultText = "举报已核实，相关内容已下架"
	}
	a.NotifyI18n(report.ReporterID, "moderation", "notify.report.resolved", map[string]string{"target": report.TargetLabel}, map[string]any{
		"link": "/notifications", "report_id": report.ID, "result": req.Resolution,
	})
	a.recordAudit(c, "report.resolved", "report", auditID(report.ID), report.TargetLabel, map[string]any{
		"target_type": report.TargetType, "target_id": report.TargetID, "resolution": req.Resolution,
	})
	ok(c, gin.H{"id": report.ID, "status": report.Status, "resolution": report.Resolution, "message": resultText})
}

func takedownReportedTarget(tx *gorm.DB, report *models.ContentReport) error {
	switch report.TargetType {
	case "book":
		result := tx.Model(&models.Book{}).Where("id = ?", report.TargetID).Updates(map[string]any{"status": "archived", "is_public": false})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	case "document":
		result := tx.Model(&models.Document{}).Where("id = ?", report.TargetID).Update("status", "archived")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	case "comment":
		result := tx.Model(&models.Comment{}).Where("id = ?", report.TargetID).Update("status", "hidden")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	default:
		return gorm.ErrRecordNotFound
	}
	return nil
}
