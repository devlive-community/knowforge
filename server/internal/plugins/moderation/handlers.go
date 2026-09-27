package moderation

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	maxWordLength   = 50
	maxWordsPerPost = 5000
)

func (b *behavior) Key() string { return plugins.KeyModeration }

func (b *behavior) RegisterRoutes(api *gin.RouterGroup, core plugincore.Core) {
	b.core = core
	feat := core.RequireFeaturePlugin(plugins.KeyModeration)
	api.GET("/users/me/moderation-cases", core.RequireAuth(), feat, core.RequirePermissionMiddleware(PermRead), b.MyCases)
	admin := []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage)}
	reg := func(method, path string, h gin.HandlerFunc) {
		api.Handle(method, path, append(append([]gin.HandlerFunc{}, admin...), h)...)
	}
	reg(http.MethodGet, "/admin/moderation/cases", b.AdminListCases)
	reg(http.MethodGet, "/admin/moderation/cases/:id/content", b.AdminCaseContent)
	reg(http.MethodPost, "/admin/moderation/cases/:id/approve", b.AdminApprove)
	reg(http.MethodPost, "/admin/moderation/cases/:id/reject", b.AdminReject)
	reg(http.MethodGet, "/admin/moderation/words", b.AdminListWords)
	reg(http.MethodPost, "/admin/moderation/words", b.AdminAddWords)
	reg(http.MethodPut, "/admin/moderation/words/:id", b.AdminUpdateWord)
	reg(http.MethodDelete, "/admin/moderation/words/:id", b.AdminDeleteWord)
	reg(http.MethodPost, "/admin/moderation/test", b.AdminTest)
	reg(http.MethodGet, "/admin/moderation/settings", b.AdminGetSettings)
	reg(http.MethodPut, "/admin/moderation/settings", b.AdminUpdateSettings)
	reg(http.MethodGet, "/admin/moderation/ai-settings", b.AdminGetAISettings)
	reg(http.MethodPut, "/admin/moderation/ai-settings", b.AdminUpdateAISettings)
	reg(http.MethodPost, "/admin/moderation/cases/:id/ai-review", b.AdminAIReview)
	api.GET("/admin/moderation/stream", core.RequireAuthStream(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(PermManage), b.AdminCasesStream)
}

func changedFields(fields ...string) map[string]any { return map[string]any{"changed_fields": fields} }

type userBrief struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// caseItems 审核记录附带作者、书籍与章节 slug（便于跳转阅读页）。
func (b *behavior) caseItems(rows []Case) []gin.H {
	db := b.core.Gorm()
	userIDs, bookIDs, docIDs := []uint{}, []uint{}, []uint{}
	for _, r := range rows {
		userIDs = append(userIDs, r.UserID)
		bookIDs = append(bookIDs, r.BookID)
		if r.Kind == plugincore.PublishDocument {
			docIDs = append(docIDs, r.TargetID)
		}
	}
	users, books, docs := map[uint]userBrief{}, map[uint]gin.H{}, map[uint]string{}
	var ul []models.User
	db.Select("id, username, nickname, avatar").Where("id IN ?", userIDs).Find(&ul)
	for _, u := range ul {
		users[u.ID] = userBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar}
	}
	var bl []models.Book
	db.Select("id, title, slug").Where("id IN ?", bookIDs).Find(&bl)
	for _, bk := range bl {
		books[bk.ID] = gin.H{"id": bk.ID, "title": bk.Title, "slug": bk.Slug}
	}
	var dl []models.Document
	db.Select("id, slug").Where("id IN ?", docIDs).Find(&dl)
	for _, d := range dl {
		docs[d.ID] = d.Slug
	}
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		item := gin.H{"case": r, "user": users[r.UserID], "book": books[r.BookID]}
		if r.Kind == plugincore.PublishDocument {
			item["doc_slug"] = docs[r.TargetID]
		} else if uc, found := plugincore.UserContentFor(r.Kind); found {
			if ref, ok := uc.Resolve(b.core, nil, r.TargetID); ok { // 其他插件登记的用户内容：由登记者提供查看链接
				item["link"] = ref.Link
			}
		}
		items = append(items, item)
	}
	return items
}

// MyCases GET /users/me/moderation-cases 我的内容审核记录（待审核/已通过/已驳回，含命中位置与复审意见）。
func (b *behavior) MyCases(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Case{}).Where("user_id = ? AND status <> ?", b.core.CurrentUser(c).ID, StatusAutoPassed)
	var total int64
	q.Count(&total)
	var rows []Case
	q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	for i := range rows { // AI 复核结果只给审核员看
		rows[i].AIStatus, rows[i].AIVerdict, rows[i].AIConfidence, rows[i].AICategories, rows[i].AIReason, rows[i].AIError = "", "", 0, nil, "", ""
		rows[i].AIModel, rows[i].AIInputTokens, rows[i].AIOutputTokens, rows[i].AIReviewedAt = "", 0, 0, nil
	}
	b.core.OK(c, plugincore.PageResult{Items: b.caseItems(rows), Total: total, Page: page, PageSize: pageSize})
}

// AdminListCases GET /admin/moderation/cases?status=&kind=&q=&page=
func (b *behavior) AdminListCases(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	db := b.core.Gorm()
	q := db.Model(&Case{})
	switch s := c.Query("status"); s {
	case StatusPending, StatusAutoPassed, StatusApproved, StatusRejected:
		q = q.Where("status = ?", s)
	case "handled":
		q = q.Where("status IN ?", []string{StatusApproved, StatusRejected})
	}
	if k := c.Query("kind"); k == plugincore.PublishDocument || k == plugincore.PublishBook || isUserContent(k) {
		q = q.Where("kind = ?", k)
	}
	switch c.Query("ai") {
	case "flagged": // AI 判定违规或不确定的未结记录
		q = q.Where("ai_verdict IN ? AND status IN ?", []string{verdictViolation, verdictUncertain}, []string{StatusPending, StatusAutoPassed})
	case "safe":
		q = q.Where("ai_verdict = ?", verdictSafe)
	}
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("title LIKE ? OR user_id IN (?)", like, db.Model(&models.User{}).Select("id").Where("username LIKE ?", like))
	}
	var total int64
	q.Count(&total)
	var rows []Case
	q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	var pending, flagged int64
	db.Model(&Case{}).Where("status = ?", StatusPending).Count(&pending)
	db.Model(&Case{}).Where("ai_verdict IN ? AND status IN ?", []string{verdictViolation, verdictUncertain}, []string{StatusPending, StatusAutoPassed}).Count(&flagged)
	b.core.OK(c, gin.H{"items": b.caseItems(rows), "total": total, "page": page, "page_size": pageSize, "pending": pending, "ai_flagged": flagged, "ai_active": b.aiActive()})
}

func (b *behavior) findCase(c *gin.Context) (*Case, bool) {
	var row Case
	if b.core.Gorm().First(&row, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "审核记录不存在")
		return nil, false
	}
	return &row, true
}

// AdminCaseContent GET /admin/moderation/cases/:id/content 对象的当前文本（供复审时对照命中位置）。
func (b *behavior) AdminCaseContent(c *gin.Context) {
	row, found := b.findCase(c)
	if !found {
		return
	}
	db := b.core.Gorm()
	fields := map[string]string{}
	switch row.Kind {
	case plugincore.PublishDocument:
		var d models.Document
		if db.First(&d, row.TargetID).Error == nil {
			fields = map[string]string{"title": d.Title, "content": d.Content}
		}
	case plugincore.PublishBook:
		var bk models.Book
		if db.First(&bk, row.TargetID).Error == nil {
			fields = map[string]string{"title": bk.Title, "description": bk.Description}
		}
	}
	if len(fields) == 0 {
		b.core.Fail(c, http.StatusNotFound, "内容已被删除")
		return
	}
	b.core.OK(c, gin.H{"fields": fields, "hits": b.scan(fields, b.settings().SkipNoise)})
}

// AdminApprove POST /admin/moderation/cases/:id/approve {note?} 复审通过（待审核内容随即发布）。
func (b *behavior) AdminApprove(c *gin.Context) {
	b.decide(c, true)
}

// AdminReject POST /admin/moderation/cases/:id/reject {note} 驳回（自动通过的内容撤回发布），命中位置与意见通知作者。
func (b *behavior) AdminReject(c *gin.Context) {
	b.decide(c, false)
}

func (b *behavior) decide(c *gin.Context, approve bool) {
	row, found := b.findCase(c)
	if !found {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > 500 {
		b.core.Fail(c, http.StatusBadRequest, "意见不能超过 500 字")
		return
	}
	if !approve && note == "" {
		b.core.Fail(c, http.StatusBadRequest, "请填写驳回意见")
		return
	}
	if row.Status != StatusPending && row.Status != StatusAutoPassed {
		b.core.Fail(c, http.StatusConflict, "该记录已处理")
		return
	}
	if err := b.review(row, b.core.CurrentUser(c).ID, approve, note); err != nil {
		b.core.Fail(c, http.StatusConflict, err.Error())
		return
	}
	if approve {
		b.core.RecordAudit(c, "moderation.case_approved", "moderation_case", strconv.FormatUint(uint64(row.ID), 10), row.Title, changedFields("status"))
	} else {
		b.core.RecordAudit(c, "moderation.case_rejected", "moderation_case", strconv.FormatUint(uint64(row.ID), 10), row.Title, changedFields("status"))
	}
	b.core.Gorm().First(row, row.ID)
	b.core.OK(c, row)
}

// —— 词典 ——

// AdminListWords GET /admin/moderation/words?q=&category=&page=
func (b *behavior) AdminListWords(c *gin.Context) {
	page, pageSize := b.core.Paginate(c)
	q := b.core.Gorm().Model(&Word{})
	if kw := strings.TrimSpace(c.Query("q")); kw != "" {
		q = q.Where("word LIKE ?", "%"+kw+"%")
	}
	if cat := strings.TrimSpace(c.Query("category")); cat != "" {
		q = q.Where("category = ?", cat)
	}
	var total int64
	q.Count(&total)
	var rows []Word
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows)
	var categories []string
	b.core.Gorm().Model(&Word{}).Distinct("category").Where("category <> ''").Order("category").Pluck("category", &categories)
	var enabled int64
	b.core.Gorm().Model(&Word{}).Where("enabled = ?", true).Count(&enabled)
	b.core.OK(c, gin.H{"items": rows, "total": total, "page": page, "page_size": pageSize, "categories": categories, "enabled_total": enabled})
}

// splitWords 批量添加：按换行/逗号/顿号分隔，去重、去空。
func splitWords(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == '，' || r == '、' || r == ';' || r == '；'
	})
	seen := map[string]bool{}
	out := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	return out
}

// AdminAddWords POST /admin/moderation/words {words, category} 批量添加（已存在的跳过），返回 {added, skipped}。
func (b *behavior) AdminAddWords(c *gin.Context) {
	var req struct {
		Words    string `json:"words"`
		Category string `json:"category"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	words := splitWords(req.Words)
	if len(words) == 0 {
		b.core.Fail(c, http.StatusBadRequest, "请填写敏感词")
		return
	}
	if len(words) > maxWordsPerPost {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("一次最多添加 %d 个", maxWordsPerPost))
		return
	}
	category := strings.TrimSpace(req.Category)
	if utf8.RuneCountInString(category) > 30 {
		b.core.Fail(c, http.StatusBadRequest, "分类不能超过 30 个字")
		return
	}
	added, skipped := 0, 0
	db := b.core.Gorm()
	for _, w := range words {
		if utf8.RuneCountInString(w) > maxWordLength {
			skipped++
			continue
		}
		var n int64
		db.Model(&Word{}).Where("word = ?", w).Count(&n)
		if n > 0 {
			skipped++
			continue
		}
		if db.Create(&Word{Word: w, Category: category, Enabled: true}).Error == nil {
			added++
		} else {
			skipped++
		}
	}
	if added > 0 {
		b.bumpDictVersion()
		b.core.RecordAudit(c, "moderation.words_added", "moderation_word", "dictionary", "敏感词词典", map[string]any{"changed_fields": []string{"words"}, "added": added})
	}
	b.core.OK(c, gin.H{"added": added, "skipped": skipped})
}

// AdminUpdateWord PUT /admin/moderation/words/:id {enabled?, category?}
func (b *behavior) AdminUpdateWord(c *gin.Context) {
	var w Word
	if b.core.Gorm().First(&w, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "敏感词不存在")
		return
	}
	var req struct {
		Enabled  *bool   `json:"enabled"`
		Category *string `json:"category"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	updates := map[string]any{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Category != nil {
		cat := strings.TrimSpace(*req.Category)
		if utf8.RuneCountInString(cat) > 30 {
			b.core.Fail(c, http.StatusBadRequest, "分类不能超过 30 个字")
			return
		}
		updates["category"] = cat
	}
	if len(updates) > 0 {
		b.core.Gorm().Model(&Word{}).Where("id = ?", w.ID).Updates(updates)
		b.bumpDictVersion()
		b.core.RecordAudit(c, "moderation.word_updated", "moderation_word", strconv.FormatUint(uint64(w.ID), 10), w.Word, changedFields("enabled", "category"))
	}
	b.core.Gorm().First(&w, w.ID)
	b.core.OK(c, w)
}

// AdminDeleteWord DELETE /admin/moderation/words/:id
func (b *behavior) AdminDeleteWord(c *gin.Context) {
	var w Word
	if b.core.Gorm().First(&w, c.Param("id")).Error != nil {
		b.core.Fail(c, http.StatusNotFound, "敏感词不存在")
		return
	}
	b.core.Gorm().Delete(&w)
	b.bumpDictVersion()
	b.core.RecordAudit(c, "moderation.word_deleted", "moderation_word", strconv.FormatUint(uint64(w.ID), 10), w.Word, changedFields("deleted"))
	b.core.OK(c, gin.H{"message": "已删除"})
}

// AdminTest POST /admin/moderation/test {text} 用当前词典与设置试审一段文本。
func (b *behavior) AdminTest(c *gin.Context) {
	var req struct {
		Text string `json:"text"`
	}
	if c.ShouldBindJSON(&req) != nil || utf8.RuneCountInString(req.Text) > 100000 {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	hits := b.scan(map[string]string{"content": req.Text}, b.settings().SkipNoise)
	if hits == nil {
		hits = Hits{}
	}
	b.core.OK(c, gin.H{"hits": hits})
}

// —— 设置 ——

// AdminGetSettings GET /admin/moderation/settings
func (b *behavior) AdminGetSettings(c *gin.Context) { b.core.OK(c, b.settings()) }

// AdminUpdateSettings PUT /admin/moderation/settings（可只传部分字段）
func (b *behavior) AdminUpdateSettings(c *gin.Context) {
	var req map[string]*bool
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	keys := map[string][2]string{
		"scope_documents": {cfgScopeDocuments, "内容审核：审查章节发布"},
		"scope_books":     {cfgScopeBooks, "内容审核：审查书籍公开"},
		"scope_ugc":       {cfgScopeUGC, "内容审核：审查用户发布的其他内容（如问答）"},
		"skip_noise":      {cfgSkipNoise, "内容审核：忽略词语间的空白与符号"},
		"notify_pass":     {cfgNotifyPass, "内容审核：自动通过时通知作者"},
		"admin_exempt":    {cfgAdminExempt, "内容审核：管理员发布免审"},
	}
	changed := []string{}
	for field, v := range req {
		def, known := keys[field]
		if !known || v == nil {
			continue
		}
		if err := b.core.SetSetting(def[0], strconv.FormatBool(*v), def[1]); err != nil {
			b.core.Fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		changed = append(changed, field)
	}
	b.core.RecordAudit(c, "moderation.settings_updated", "moderation", "settings", "内容审核设置", changedFields(changed...))
	b.core.OK(c, b.settings())
}

func isUserContent(kind string) bool {
	_, found := plugincore.UserContentFor(kind)
	return found
}
