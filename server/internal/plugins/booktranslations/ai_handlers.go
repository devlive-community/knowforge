package booktranslations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/authz"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

const (
	featureBookTranslate = "translate.book" // AI 用量记录中的功能名（计入每月翻译字数）
	maxGlossaryTerms     = 500
	maxTermRunes         = 200
	maxInstructionRunes  = 1000
	streamHeartbeat      = 25 * time.Second
)

func (b *behavior) registerAIRoutes(api *gin.RouterGroup, core plugincore.Core) {
	feat := core.RequireFeaturePlugin(plugins.KeyBookTranslations)
	use := core.RequirePermissionMiddleware(PermAITranslate)
	with := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), feat, use, h}
	}
	api.GET("/books/:id/ai-translate", with(b.AIOverview)...)
	api.GET("/books/:id/ai-translate/glossary", with(b.GetGlossary)...)
	api.PUT("/books/:id/ai-translate/glossary", with(b.PutGlossary)...)
	api.POST("/books/:id/ai-translate/jobs", with(b.CreateJob)...)
	api.GET("/books/:id/ai-translate/preset", with(b.PresetForBook)...)
	admin := func(h gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{core.RequireAuth(), core.RequireAdmin(), feat, core.RequirePermissionMiddleware(authz.SiteUpdate), h}
	}
	api.GET("/admin/book-translations/presets", admin(b.AdminGetPresets)...)
	api.PUT("/admin/book-translations/presets", admin(b.AdminUpdatePresets)...)
	api.GET("/ai-translate/jobs/:id", with(b.GetJob)...)
	api.GET("/ai-translate/jobs/:id/stream", core.RequireAuthStream(), feat, use, b.StreamJob)
	api.POST("/ai-translate/jobs/:id/pause", with(b.PauseJob)...)
	api.POST("/ai-translate/jobs/:id/resume", with(b.ResumeJob)...)
	api.POST("/ai-translate/jobs/:id/retry", with(b.RetryJob)...)
}

// sourceBook 当前用户可编辑内容的原书。
func (b *behavior) sourceBook(c *gin.Context) (*models.Book, *models.User, bool) {
	book, status := b.core.FindBook(c)
	if book == nil {
		b.core.Fail(c, status, "书籍不存在")
		return nil, nil, false
	}
	u := b.core.CurrentUser(c)
	if !b.core.CanEditBookContent(u, book) {
		b.core.Fail(c, http.StatusForbidden, "没有编辑这本书的权限")
		return nil, nil, false
	}
	return book, u, true
}

func (b *behavior) aiAllowed(u *models.User) bool {
	return plugincore.EntitlementValue(b.core, u, entAIBook) > 0
}

type sourceDoc struct {
	doc   models.Document
	depth int
}

// sourceDocs 原书全部章节（目录先序：父章节在子章节之前，同级按排序）。
func (b *behavior) sourceDocs(bookID uint) []sourceDoc {
	var docs []models.Document
	b.core.Gorm().Where("book_id = ?", bookID).Order("sort_order ASC, id ASC").Find(&docs)
	children := map[uint][]models.Document{}
	ids := map[uint]bool{}
	for _, d := range docs {
		ids[d.ID] = true
	}
	var roots []models.Document
	for _, d := range docs {
		if d.ParentID != nil && ids[*d.ParentID] {
			children[*d.ParentID] = append(children[*d.ParentID], d)
		} else {
			roots = append(roots, d)
		}
	}
	out := make([]sourceDoc, 0, len(docs))
	var walk func(list []models.Document, depth int)
	walk = func(list []models.Document, depth int) {
		for _, d := range list {
			out = append(out, sourceDoc{doc: d, depth: depth})
			walk(children[d.ID], depth+1)
		}
	}
	walk(roots, 0)
	return out
}

func docChars(d *models.Document) int64 { return runeLen(d.Title) + runeLen(d.Content) }

type targetView struct {
	Book        gin.H         `json:"book"`
	TargetLang  string        `json:"target_lang"`
	TargetLabel string        `json:"target_label"`
	Changed     int           `json:"changed"` // 原文修改后尚未同步的章节
	Added       int           `json:"added"`   // 原文新增、译本中还没有的章节
	Chars       int64         `json:"chars"`   // 同步需要翻译的原文字数（估算）
	LastJob     *TranslateJob `json:"last_job"`
}

// AIOverview GET /books/:id/ai-translate 整本 AI 翻译概况：是否可用、本月剩余翻译字数、原书规模、已有译本（含待同步章节）与最近任务。
func (b *behavior) AIOverview(c *gin.Context) {
	book, u, ok := b.sourceBook(c)
	if !ok {
		return
	}
	db := b.core.Gorm()
	chat, _ := b.core.AIStatus()
	docs := b.sourceDocs(book.ID)
	var chars int64
	for i := range docs {
		chars += docChars(&docs[i].doc)
	}
	var targetIDs []uint
	db.Model(&TranslateJob{}).Where("source_book_id = ?", book.ID).Distinct().Pluck("target_book_id", &targetIDs)
	targets := []targetView{}
	for _, id := range targetIDs {
		var dst models.Book
		if db.First(&dst, id).Error != nil || !b.core.CanEditBookContent(u, &dst) {
			continue
		}
		var last TranslateJob
		db.Where("source_book_id = ? AND target_book_id = ?", book.ID, id).Order("id DESC").First(&last)
		changed, added, need := b.pending(&dst, docs)
		targets = append(targets, targetView{
			Book:       gin.H{"id": dst.ID, "slug": dst.Slug, "title": dst.Title, "language": dst.Language, "status": dst.Status},
			TargetLang: last.TargetLang, TargetLabel: last.TargetLabel, Changed: len(changed), Added: len(added), Chars: need, LastJob: &last,
		})
	}
	jobs := []TranslateJob{}
	db.Where("source_book_id = ?", book.ID).Order("id DESC").Limit(20).Find(&jobs)
	b.core.OK(c, gin.H{
		"available": chat, "allowed": b.aiAllowed(u), "chars_left": b.core.TranslateCharsLeft(u),
		"source":  gin.H{"chapters": len(docs), "chars": chars, "language": book.Language},
		"targets": targets, "jobs": jobs,
	})
}

// pending 原书相对某个译本的待同步章节：changed 为已翻译但原文已修改，added 为译本中还没有的章节；need 为需要翻译的原文字数。
func (b *behavior) pending(dst *models.Book, docs []sourceDoc) (changed, added []sourceDoc, need int64) {
	var rows []TranslatedDoc
	b.core.Gorm().Where("target_book_id = ?", dst.ID).Find(&rows)
	byDoc := map[uint]TranslatedDoc{}
	for _, row := range rows {
		byDoc[row.SourceDocID] = row
	}
	for _, d := range docs {
		row, has := byDoc[d.doc.ID]
		var exists int64
		if has {
			b.core.Gorm().Model(&models.Document{}).Where("id = ? AND book_id = ?", row.TargetDocID, dst.ID).Count(&exists)
		}
		switch {
		case !has || exists == 0:
			added = append(added, d)
			need += docChars(&d.doc)
		case row.SourceHash != sourceHash(&d.doc):
			changed = append(changed, d)
			need += docChars(&d.doc)
		}
	}
	return
}

type glossaryInput struct {
	Lang  string `json:"lang"`
	Terms []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"terms"`
}

// GetGlossary GET /books/:id/ai-translate/glossary?lang= 原书某个目标语言的术语表。
func (b *behavior) GetGlossary(c *gin.Context) {
	book, _, ok := b.sourceBook(c)
	if !ok {
		return
	}
	terms := []GlossaryTerm{}
	b.core.Gorm().Where("book_id = ? AND target_lang = ?", book.ID, strings.TrimSpace(c.Query("lang"))).Order("id ASC").Find(&terms)
	b.core.OK(c, gin.H{"terms": terms})
}

// PutGlossary PUT /books/:id/ai-translate/glossary {lang, terms:[{source, target}]} 整体替换术语表。
func (b *behavior) PutGlossary(c *gin.Context) {
	book, _, ok := b.sourceBook(c)
	if !ok {
		return
	}
	var req glossaryInput
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Lang) == "" || len(req.Lang) > 16 {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if len(req.Terms) > maxGlossaryTerms {
		b.core.Fail(c, http.StatusBadRequest, fmt.Sprintf("术语表最多 %d 条", maxGlossaryTerms))
		return
	}
	terms := make([]GlossaryTerm, 0, len(req.Terms))
	seen := map[string]bool{}
	for _, t := range req.Terms {
		src, dst := strings.TrimSpace(t.Source), strings.TrimSpace(t.Target)
		if src == "" && dst == "" {
			continue
		}
		if src == "" || dst == "" || utf8.RuneCountInString(src) > maxTermRunes || utf8.RuneCountInString(dst) > maxTermRunes {
			b.core.Fail(c, http.StatusBadRequest, "每条术语需填写原文与译文（各不超过 200 字）")
			return
		}
		if seen[src] {
			b.core.Fail(c, http.StatusBadRequest, "术语重复："+src)
			return
		}
		seen[src] = true
		terms = append(terms, GlossaryTerm{BookID: book.ID, TargetLang: strings.TrimSpace(req.Lang), Source: src, Target: dst})
	}
	db := b.core.Gorm()
	db.Where("book_id = ? AND target_lang = ?", book.ID, strings.TrimSpace(req.Lang)).Delete(&GlossaryTerm{})
	if len(terms) > 0 {
		db.Create(&terms)
	}
	b.core.OK(c, gin.H{"terms": terms})
}

// CreateJob POST /books/:id/ai-translate/jobs {target_lang, target_label, title?, slug?, instructions?, target_book_id?}
// 新建译本时 title / slug 不传则按预设模板生成（见 presets.go）；title 传空串表示由 AI 翻译原书名，slug 传空串表示自动生成。
// 不带 target_book_id 时新建译本（私有草稿书，与原书同一翻译分组）并翻译全书；带 target_book_id 时同步该译本（只翻译原文新增或修改的章节）。
func (b *behavior) CreateJob(c *gin.Context) {
	book, u, ok := b.sourceBook(c)
	if !ok {
		return
	}
	var req struct {
		TargetLang   string  `json:"target_lang"`
		TargetLabel  string  `json:"target_label"`
		Title        *string `json:"title"`
		Slug         *string `json:"slug"`
		Instructions string  `json:"instructions"`
		TargetBookID uint    `json:"target_book_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		b.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.TargetLang, req.TargetLabel, req.Instructions = strings.TrimSpace(req.TargetLang), strings.TrimSpace(req.TargetLabel), strings.TrimSpace(req.Instructions)
	if utf8.RuneCountInString(req.Instructions) > maxInstructionRunes {
		b.core.Fail(c, http.StatusBadRequest, "翻译要求不能超过 1000 字")
		return
	}
	if chat, _ := b.core.AIStatus(); !chat {
		b.core.Fail(c, http.StatusServiceUnavailable, "AI 服务暂未配置")
		return
	}
	if !b.aiAllowed(u) {
		b.core.Fail(c, http.StatusForbidden, "当前等级/会员不含整本 AI 翻译，提升等级或开通会员后可用")
		return
	}
	db := b.core.Gorm()
	docs := b.sourceDocs(book.ID)
	job := TranslateJob{UserID: u.ID, SourceBookID: book.ID, Status: jobRunning, Stage: stageOutline, Instructions: req.Instructions, TraceID: ai.NewTraceID()}
	var todo []sourceDoc
	changedIDs := map[uint]bool{}
	var dst models.Book
	wantSlug := "" // 新建译本的访问路径（空则沿用自动生成的）
	if req.TargetBookID != 0 {
		// 同步：沿用上次任务的目标语言与要求（未指定时）
		var last TranslateJob
		if db.First(&dst, req.TargetBookID).Error != nil || db.Where("source_book_id = ? AND target_book_id = ?", book.ID, dst.ID).Order("id DESC").First(&last).Error != nil {
			b.core.Fail(c, http.StatusNotFound, "译本不存在")
			return
		}
		if !b.core.CanEditBookContent(u, &dst) {
			b.core.Fail(c, http.StatusForbidden, "没有编辑译本的权限")
			return
		}
		var busy int64
		db.Model(&TranslateJob{}).Where("target_book_id = ? AND status IN ?", dst.ID, []string{jobRunning, jobPaused}).Count(&busy)
		if busy > 0 {
			b.core.Fail(c, http.StatusConflict, "该译本还有未完成的翻译任务，请先继续或等待完成")
			return
		}
		job.Mode, job.TargetBookID, job.TargetLang, job.TargetLabel = modeSync, dst.ID, last.TargetLang, last.TargetLabel
		if job.Instructions == "" {
			job.Instructions = last.Instructions
		}
		changed, added, _ := b.pending(&dst, docs)
		for _, d := range changed {
			changedIDs[d.doc.ID] = true
		}
		todo = append(todo, added...)
		todo = append(todo, changed...)
		if len(todo) == 0 {
			b.core.Fail(c, http.StatusBadRequest, "译本已是最新，没有需要同步的章节")
			return
		}
	} else {
		if req.TargetLang == "" || req.TargetLabel == "" || len(req.TargetLang) > 16 || utf8.RuneCountInString(req.TargetLabel) > 64 {
			b.core.Fail(c, http.StatusBadRequest, "请选择目标语言")
			return
		}
		if len(docs) == 0 {
			b.core.Fail(c, http.StatusBadRequest, "这本书还没有章节")
			return
		}
		presetTitle, presetSlug := b.presets().presetFor(req.TargetLang).render(book, req.TargetLang, req.TargetLabel)
		title := presetTitle
		if req.Title != nil {
			title = strings.TrimSpace(*req.Title)
		}
		wantSlug = b.availableSlug(presetSlug)
		if req.Slug != nil {
			wantSlug = strings.TrimSpace(*req.Slug)
			if wantSlug != "" && !bookSlugRe.MatchString(wantSlug) {
				b.core.Fail(c, http.StatusBadRequest, "访问路径仅支持小写字母、数字和中划线")
				return
			}
			if wantSlug != "" && b.slugTaken(wantSlug, 0) {
				b.core.Fail(c, http.StatusConflict, "访问路径已被占用")
				return
			}
		}
		job.Mode, job.TargetLang, job.TargetLabel, job.BookTitle = modeFull, req.TargetLang, req.TargetLabel, truncate(title, 255)
		todo = docs
	}
	var need int64
	for i := range todo {
		need += docChars(&todo[i].doc)
	}
	if left := b.core.TranslateCharsLeft(u); left >= 0 && left < need {
		b.core.Fail(c, http.StatusTooManyRequests, fmt.Sprintf("本月翻译字数不足：需要约 %d 字，剩余 %d 字；下月恢复，或提升等级/开通会员获得更多额度", need, left))
		return
	}
	if err := b.core.AICheckQuota(ai.WithCaller(c.Request.Context(), ai.Caller{UserID: u.ID, Feature: featureBookTranslate})); err != nil {
		b.core.Fail(c, http.StatusTooManyRequests, err.Error())
		return
	}
	if job.Mode == modeFull {
		// 新建译本：私有草稿书，沿用原书配置；与原书同一翻译分组（原书未分组时以其访问路径作为分组标识）
		if strings.TrimSpace(book.TransGroup) == "" {
			book.TransGroup = truncate(book.Slug, 64)
			db.Model(&models.Book{}).Where("id = ?", book.ID).Update("trans_group", book.TransGroup)
		}
		title := job.BookTitle
		if title == "" {
			title = book.Title + "（" + job.TargetLabel + "）" // 目录翻译完成后替换为译名
		}
		created, err := b.core.CreateDraftBookFrom(u, book, title)
		if err != nil {
			b.core.Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		dst = created
		updates := map[string]any{"language": truncate(job.TargetLabel, 32), "trans_group": book.TransGroup}
		if wantSlug != "" && !b.slugTaken(wantSlug, dst.ID) {
			updates["slug"], dst.Slug = wantSlug, wantSlug
		}
		db.Model(&models.Book{}).Where("id = ?", dst.ID).Updates(updates)
		job.TargetBookID = dst.ID
	}
	job.Total = len(todo)
	if err := db.Create(&job).Error; err != nil {
		b.core.Fail(c, http.StatusInternalServerError, "创建翻译任务失败")
		return
	}
	items := make([]TranslateItem, 0, len(todo))
	var mapping []TranslatedDoc
	db.Where("target_book_id = ?", dst.ID).Find(&mapping)
	targetOf := map[uint]uint{}
	for _, m := range mapping {
		targetOf[m.SourceDocID] = m.TargetDocID
	}
	for i, d := range todo {
		it := TranslateItem{JobID: job.ID, SourceDocID: d.doc.ID, Ord: i, Depth: d.depth, Title: d.doc.Title, Status: itemPending}
		if changedIDs[d.doc.ID] {
			it.TargetDocID = targetOf[d.doc.ID] // 已有译本章节：只更新内容，标题重新翻译
		}
		items = append(items, it)
	}
	if job.Mode == modeSync {
		// 同步时新增章节需按目录先序建立（父章节在前），其余按原顺序
		sortByOrd(items, docs)
	}
	db.Create(&items)
	b.core.RecordAudit(c, "book.ai_translate", "book", book.Slug, book.Title, map[string]any{"mode": job.Mode, "target_book": dst.Slug, "target_lang": job.TargetLang, "chapters": job.Total})
	b.startJob(job)
	b.core.OK(c, jobView{Job: job, Items: items})
}

// sortByOrd 按原书目录先序重排任务章节并重设处理顺序。
func sortByOrd(items []TranslateItem, docs []sourceDoc) {
	pos := make(map[uint]int, len(docs))
	for i, d := range docs {
		pos[d.doc.ID] = i
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && pos[items[j].SourceDocID] < pos[items[j-1].SourceDocID]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	for i := range items {
		items[i].Ord = i
	}
}

type jobView struct {
	Job     TranslateJob    `json:"job"`
	Items   []TranslateItem `json:"items"`
	Current *deltaEvent     `json:"current,omitempty"` // 进行中：当前章节已生成的译文（后续 delta 按 seq 去重）
	// CurrentSync 进行中且在其他实例上执行：快照不含当前译文，随后推送 partial 补齐（此前的 reset/delta 应忽略）
	CurrentSync bool `json:"current_sync,omitempty"`
}

// myJob 当前用户发起的任务（管理员可查看全部）。
func (b *behavior) myJob(c *gin.Context) (TranslateJob, bool) {
	var job TranslateJob
	u := b.core.CurrentUser(c)
	if b.core.Gorm().First(&job, c.Param("id")).Error != nil || (job.UserID != u.ID && !b.core.IsAdmin(u)) {
		b.core.Fail(c, http.StatusNotFound, "翻译任务不存在")
		return job, false
	}
	return job, true
}

func (b *behavior) view(job TranslateJob) jobView {
	items := []TranslateItem{}
	b.core.Gorm().Where("job_id = ?", job.ID).Order("ord ASC").Find(&items)
	return jobView{Job: job, Items: items}
}

// GetJob GET /ai-translate/jobs/:id 任务详情与各章节状态。
func (b *behavior) GetJob(c *gin.Context) {
	if job, ok := b.myJob(c); ok {
		b.core.OK(c, b.view(job))
	}
}

// PauseJob POST /ai-translate/jobs/:id/pause 暂停（当前章节未完成的部分不保存，继续时重新翻译该章）。
func (b *behavior) PauseJob(c *gin.Context) {
	job, ok := b.myJob(c)
	if !ok {
		return
	}
	if job.Status != jobRunning || !runningJobs.Cancel(job.ID, job.Runner) { // 可能在其他实例上执行
		b.core.Fail(c, http.StatusConflict, "任务不在进行中")
		return
	}
	b.core.OK(c, gin.H{"paused": true})
}

// ResumeJob POST /ai-translate/jobs/:id/resume 继续已暂停的任务。
func (b *behavior) ResumeJob(c *gin.Context) {
	job, ok := b.myJob(c)
	if !ok {
		return
	}
	if job.Status != jobPaused {
		b.core.Fail(c, http.StatusConflict, "只有已暂停的任务可以继续")
		return
	}
	b.restart(c, job)
}

// RetryJob POST /ai-translate/jobs/:id/retry 重新翻译失败的章节。
func (b *behavior) RetryJob(c *gin.Context) {
	job, ok := b.myJob(c)
	if !ok {
		return
	}
	if job.Status == jobRunning {
		b.core.Fail(c, http.StatusConflict, "任务进行中")
		return
	}
	db := b.core.Gorm()
	res := db.Model(&TranslateItem{}).Where("job_id = ? AND status = ?", job.ID, itemFailed).Updates(map[string]any{"status": itemPending, "error": ""})
	if res.RowsAffected == 0 {
		b.core.Fail(c, http.StatusBadRequest, "没有失败的章节")
		return
	}
	db.Model(&TranslateJob{}).Where("id = ?", job.ID).Update("failed", gorm.Expr("failed - ?", res.RowsAffected))
	if job.Stage == stageDone {
		job.Stage = stageContent
		var missing int64
		db.Model(&TranslateItem{}).Where("job_id = ? AND target_doc_id = 0", job.ID).Count(&missing)
		if missing > 0 {
			job.Stage = stageOutline
		}
		db.Model(&TranslateJob{}).Where("id = ?", job.ID).Updates(map[string]any{"stage": job.Stage, "finished_at": nil})
	}
	b.restart(c, job)
}

func (b *behavior) restart(c *gin.Context, job TranslateJob) {
	u := b.core.CurrentUser(c)
	if chat, _ := b.core.AIStatus(); !chat {
		b.core.Fail(c, http.StatusServiceUnavailable, "AI 服务暂未配置")
		return
	}
	var owner models.User
	if b.core.Gorm().First(&owner, job.UserID).Error != nil || (owner.ID != u.ID && !b.core.IsAdmin(u)) {
		b.core.Fail(c, http.StatusNotFound, "翻译任务不存在")
		return
	}
	if left := b.core.TranslateCharsLeft(&owner); left == 0 {
		b.core.Fail(c, http.StatusTooManyRequests, errCharsExhausted.Error())
		return
	}
	b.core.Gorm().Model(&TranslateJob{}).Where("id = ?", job.ID).Updates(map[string]any{"status": jobRunning, "error": ""})
	job.Status, job.Error = jobRunning, ""
	b.startJob(job)
	b.core.OK(c, b.view(job))
}

// StreamJob GET /ai-translate/jobs/:id/stream（?ticket= 事件流凭证鉴权）任务进度：
// 先推 snapshot {job, items, current}，之后推 job（任务合计）、item（章节状态）、reset/delta（当前章节的译文片段，按 seq 去重），结束或暂停时推 done 后关闭。
func (b *behavior) StreamJob(c *gin.Context) {
	job, ok := b.myJob(c)
	if !ok {
		return
	}
	eventhub.StartSSE(c)
	ch := jobsHub.Subscribe(job.ID)
	defer jobsHub.Unsubscribe(job.ID, ch)
	b.core.Gorm().First(&job, job.ID)
	view := b.view(job)
	remote := false
	if v, live := runningJobs.Load(job.ID); live && job.Status == jobRunning {
		cur := v.(*jobRun).snapshot()
		view.Current = &cur
	} else if job.Status == jobRunning {
		view.CurrentSync, remote = true, true // 在其他实例上执行：请求其推送 partial
	}
	snapshot, _ := json.Marshal(view)
	eventhub.Write(c.Writer, "snapshot", snapshot)
	if remote {
		runningJobs.RequestPartial(job.ID)
	}
	if job.Status != jobRunning {
		eventhub.Write(c.Writer, "done", snapshot)
		return
	}
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			eventhub.Write(c.Writer, ev.Name, ev.Data)
			if ev.Name == "done" {
				return
			}
		case <-heartbeat.C:
			var latest TranslateJob
			if b.core.Gorm().First(&latest, job.ID).Error == nil && latest.Status != jobRunning {
				final, _ := json.Marshal(b.view(latest))
				eventhub.Write(c.Writer, "done", final)
				return
			}
			eventhub.Ping(c.Writer)
		}
	}
}
