package booktranslations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/cluster"
	"knowforge/server/internal/eventhub"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/taskrun"
)

// 整本 AI 翻译在后台逐章进行：先翻译目录并在译本中建好全部章节（草稿），再逐章翻译正文；
// 不设整体超时，每一步经 SSE 推送（见 ai_stream.go），可暂停/继续，失败的章节可重试。
// 进程内登记进行中的任务，服务重启后遗留的进行中任务由巡检标记为已暂停，可继续。

var (
	runningJobs = taskrun.New("booktranslations.jobs") // 任务 ID → *jobRun
	jobsHub     = eventhub.New("booktranslations.jobs", 1024)
)

// segmentRunes 每次调用翻译的原文长度（按段落切分，单个段落或代码块不拆开）。
const segmentRunes = 3000

var errCharsExhausted = errors.New("本月翻译字数已用完，下月恢复，或提升等级/开通会员获得更多额度；可稍后继续")

// jobRun 进行中任务的内存状态：取消函数与当前章节已生成的译文（快照给中途连接的页面）。
type jobRun struct {
	id      uint
	cancel  context.CancelFunc
	mu      sync.Mutex
	itemID  uint
	partial strings.Builder
	seq     int
}

type deltaEvent struct {
	ItemID uint   `json:"item_id"`
	Seq    int    `json:"seq"`
	Text   string `json:"text,omitempty"`
}

// begin 开始翻译一个章节：清空临时译文并推送 reset。
func (r *jobRun) begin(itemID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.itemID = itemID
	r.partial.Reset()
	r.seq++
	jobsHub.Publish(r.id, "reset", deltaEvent{ItemID: itemID, Seq: r.seq})
}

func (r *jobRun) append(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	r.partial.WriteString(text)
	jobsHub.Publish(r.id, "delta", deltaEvent{ItemID: r.itemID, Seq: r.seq, Text: text})
}

// Cancel 暂停任务（taskrun.Runner）。
func (r *jobRun) Cancel() { r.cancel() }

// PublishPartial 推送当前章节的完整部分译文（在锁内推送，与 delta 保持顺序），供其他实例上的订阅者同步。
func (r *jobRun) PublishPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()
	jobsHub.Publish(r.id, "partial", deltaEvent{ItemID: r.itemID, Seq: r.seq, Text: r.partial.String()})
}

func (r *jobRun) snapshot() deltaEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return deltaEvent{ItemID: r.itemID, Seq: r.seq, Text: r.partial.String()}
}

// sourceHash 原文章节（标题 + 正文 + 外链）的摘要。
func sourceHash(d *models.Document) string {
	sum := sha256.Sum256([]byte(d.Title + "\x00" + d.Content + "\x00" + d.ExternalURL))
	return hex.EncodeToString(sum[:])
}

func runeLen(s string) int64 { return int64(utf8.RuneCountInString(s)) }

// splitSegments 按空行把 Markdown 切成若干段（围栏代码块内的空行不切），再合并为不超过 max 字的片段。
// code 标记该片段是否只由代码块组成（原样保留，不翻译）。
type segment struct {
	text string
	code bool
}

func splitSegments(content string, max int) []segment {
	var blocks []string
	var cur []string
	fence := ""
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if fence == "" && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			fence = trimmed[:3]
		} else if fence != "" && strings.HasPrefix(trimmed, fence) {
			fence = ""
			cur = append(cur, line)
			continue
		}
		if fence == "" && trimmed == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()

	isCode := func(block string) bool {
		t := strings.TrimSpace(block)
		return (strings.HasPrefix(t, "```") && strings.HasSuffix(t, "```")) || (strings.HasPrefix(t, "~~~") && strings.HasSuffix(t, "~~~"))
	}
	var out []segment
	var buf []string
	size := 0
	emit := func() {
		if len(buf) > 0 {
			out = append(out, segment{text: strings.Join(buf, "\n\n")})
			buf, size = nil, 0
		}
	}
	for _, block := range blocks {
		if isCode(block) {
			emit()
			out = append(out, segment{text: block, code: true})
			continue
		}
		n := utf8.RuneCountInString(block)
		if size > 0 && size+n > max {
			emit()
		}
		buf = append(buf, block)
		size += n
	}
	emit()
	return out
}

func glossaryText(terms []GlossaryTerm) string {
	if len(terms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n术语表（必须统一采用）：\n")
	for _, t := range terms {
		b.WriteString("- " + t.Source + " → " + t.Target + "\n")
	}
	return b.String()
}

func contentSystemPrompt(job *TranslateJob, bookTitle, docTitle string, terms []GlossaryTerm) string {
	var b strings.Builder
	b.WriteString("你是专业的书籍译者。把用户给出的 Markdown 文本翻译成「" + job.TargetLabel + "」。要求：\n")
	b.WriteString("- 只输出译文，不要任何解释，不要用代码块包裹整个结果；\n")
	b.WriteString("- 保持 Markdown 结构完全一致：标题层级、列表、表格、引用、强调、链接与图片的地址、HTML 标签以及 ::: 提示块等组件语法原样保留，只翻译其中的文字；\n")
	b.WriteString("- 代码块与行内代码不翻译（代码块中的注释可以翻译）；\n")
	b.WriteString("- 译文准确、通顺、符合目标语言的表达习惯，专有名词前后一致。\n")
	if job.Instructions != "" {
		b.WriteString("作者的要求：" + job.Instructions + "\n")
	}
	b.WriteString(glossaryText(terms))
	b.WriteString("\n书名：" + bookTitle)
	if docTitle != "" {
		b.WriteString("\n当前章节：" + docTitle)
	}
	return b.String()
}

// cleanOutput 去掉模型偶尔包裹在整个结果外的代码围栏。
func cleanOutput(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") && strings.HasSuffix(t, "```") && strings.Count(t, "```") == 2 {
		if nl := strings.Index(t, "\n"); nl > 0 {
			return strings.TrimSpace(t[nl+1 : len(t)-3])
		}
	}
	return t
}

// usageTotals 一次或多次调用的消耗合计。
type usageTotals struct {
	chars, in, out int64
	estimated      bool
}

func (u *usageTotals) add(chars int64, usage ai.Usage) {
	u.chars += chars
	u.in += usage.InputTokens
	u.out += usage.OutputTokens
	u.estimated = u.estimated || usage.Estimated
}

type runner struct {
	b     *behavior
	job   *TranslateJob
	user  *models.User
	src   *models.Book
	dst   *models.Book
	terms []GlossaryTerm
	run   *jobRun
}

// translate 翻译一段文字（按原文字符计入每月翻译字数；额度不足时返回 errCharsExhausted）。
func (r *runner) translate(ctx context.Context, system, text string, onDelta func(string), total *usageTotals) (string, error) {
	chars := runeLen(text)
	if left := r.b.core.TranslateCharsLeft(r.user); left >= 0 && left < chars {
		return "", errCharsExhausted
	}
	resp, err := r.b.core.AITranslateStream(ctx, ai.ChatRequest{System: system, Messages: []ai.Message{{Role: "user", Content: text}}, Temperature: 0.2}, chars, onDelta)
	if err != nil {
		return "", err
	}
	total.add(chars, resp.Usage)
	return cleanOutput(resp.Content), nil
}

// translateTitles 一次翻译一组短文本（书名、简介、章节标题），返回与输入等长的译文；解析失败时逐条翻译。
func (r *runner) translateTitles(ctx context.Context, texts []string, total *usageTotals) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	raw, _ := json.Marshal(texts)
	system := "你是专业的书籍译者。把用户给出的 JSON 字符串数组中的每一项（书名、简介或章节标题）翻译成「" + r.job.TargetLabel + "」。" +
		"只输出 JSON 字符串数组，顺序与数量与输入完全一致，不要任何解释。" + glossaryText(r.terms)
	out, err := r.translate(ctx, system, string(raw), nil, total)
	if err != nil {
		return nil, err
	}
	if start, end := strings.Index(out, "["), strings.LastIndex(out, "]"); start >= 0 && end > start {
		var parsed []string
		if json.Unmarshal([]byte(out[start:end+1]), &parsed) == nil && len(parsed) == len(texts) {
			return parsed, nil
		}
	}
	single := "你是专业的书籍译者。把用户给出的文字翻译成「" + r.job.TargetLabel + "」，只输出译文。" + glossaryText(r.terms)
	result := make([]string, len(texts))
	for i, t := range texts {
		if result[i], err = r.translate(ctx, single, t, nil, total); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *runner) db() *gorm.DB { return r.b.core.Gorm() }

// addJobUsage 累加任务消耗并推送任务状态。
func (r *runner) addJobUsage(total usageTotals) {
	r.db().Model(&TranslateJob{}).Where("id = ?", r.job.ID).Updates(map[string]any{
		"chars": gorm.Expr("chars + ?", total.chars), "input_tokens": gorm.Expr("input_tokens + ?", total.in),
		"output_tokens": gorm.Expr("output_tokens + ?", total.out), "estimated": gorm.Expr("estimated OR ?", total.estimated),
	})
	r.publishJob()
}

func (r *runner) publishJob() {
	var job TranslateJob
	if r.db().First(&job, r.job.ID).Error == nil {
		*r.job = job
		jobsHub.Publish(job.ID, "job", job)
		publishUserTask(r.b.core, &job)
	}
}

func (r *runner) publishItem(id uint) {
	var item TranslateItem
	if r.db().First(&item, id).Error == nil {
		jobsHub.Publish(r.job.ID, "item", item)
	}
}

// outline 翻译目录并在译本中建立尚未建立的章节（草稿，保持原书结构与访问路径）。
func (r *runner) outline(ctx context.Context) error {
	var items []TranslateItem
	r.db().Where("job_id = ? AND target_doc_id = 0", r.job.ID).Order("ord ASC").Find(&items)
	texts := []string{}
	metaN := 0
	if r.job.Mode == modeFull {
		if r.job.BookTitle == "" {
			texts = append(texts, r.src.Title)
		}
		if strings.TrimSpace(r.src.Description) != "" {
			texts = append(texts, r.src.Description)
		}
		metaN = len(texts)
	}
	for _, it := range items {
		texts = append(texts, it.Title)
	}
	var total usageTotals
	translated, err := r.translateTitles(ctx, texts, &total)
	r.addJobUsage(total)
	if err != nil {
		return err
	}
	if r.job.Mode == modeFull && metaN > 0 {
		updates := map[string]any{}
		i := 0
		if r.job.BookTitle == "" {
			updates["title"] = truncate(translated[0], 255)
			i++
		}
		if i < metaN {
			updates["description"] = truncate(translated[i], 1000)
		}
		r.db().Model(&models.Book{}).Where("id = ?", r.dst.ID).Updates(updates)
	}
	mapping := r.targetMapping()
	for i := range items {
		it := &items[i]
		title := strings.TrimSpace(translated[metaN+i])
		if title == "" {
			title = it.Title
		}
		var src models.Document
		if r.db().First(&src, it.SourceDocID).Error != nil {
			r.failItem(it, "原文章节已删除")
			continue
		}
		var parentID *uint
		if src.ParentID != nil {
			if pid, ok := mapping[*src.ParentID]; ok {
				parentID = &pid
			}
		}
		base := src.Slug
		if base == "" {
			base = r.b.core.Slugify(title)
		}
		doc := models.Document{BookID: r.dst.ID, UserID: r.user.ID, Title: truncate(title, 255), Status: "draft", ParentID: parentID,
			SortOrder: src.SortOrder, AllowComments: src.AllowComments, Icon: src.Icon, ExternalURL: src.ExternalURL, ExternalNewTab: src.ExternalNewTab}
		doc.Slug = r.b.core.UniqueChildSlug(r.dst.ID, parentID, base, 0)
		if err := r.db().Create(&doc).Error; err != nil {
			r.failItem(it, "建立译本章节失败")
			continue
		}
		mapping[src.ID] = doc.ID
		r.db().Create(&TranslatedDoc{TargetBookID: r.dst.ID, SourceDocID: src.ID, TargetDocID: doc.ID})
		r.db().Model(it).Updates(map[string]any{"target_doc_id": doc.ID, "target_title": doc.Title})
		r.publishItem(it.ID)
	}
	r.db().Model(&TranslateJob{}).Where("id = ?", r.job.ID).Update("stage", stageContent)
	r.publishJob()
	return nil
}

// targetMapping 译本中 原文章节 ID → 译本章节 ID（译本章节已被删除的不计）。
func (r *runner) targetMapping() map[uint]uint {
	var rows []TranslatedDoc
	r.db().Where("target_book_id = ?", r.dst.ID).Find(&rows)
	out := make(map[uint]uint, len(rows))
	for _, row := range rows {
		var n int64
		r.db().Model(&models.Document{}).Where("id = ? AND book_id = ?", row.TargetDocID, r.dst.ID).Count(&n)
		if n > 0 {
			out[row.SourceDocID] = row.TargetDocID
		}
	}
	return out
}

func (r *runner) failItem(it *TranslateItem, msg string) {
	r.db().Model(it).Updates(map[string]any{"status": itemFailed, "error": msg})
	r.db().Model(&TranslateJob{}).Where("id = ?", r.job.ID).Update("failed", gorm.Expr("failed + 1"))
	r.publishItem(it.ID)
	r.publishJob()
}

// chapter 翻译一个章节的正文并写入译本章节（记录版本；已发布的章节交发布守卫审查）。
func (r *runner) chapter(ctx context.Context, it *TranslateItem) error {
	started := time.Now()
	var src, dst models.Document
	if r.db().First(&src, it.SourceDocID).Error != nil {
		r.failItem(it, "原文章节已删除")
		return nil
	}
	if r.db().Where("id = ? AND book_id = ?", it.TargetDocID, r.dst.ID).First(&dst).Error != nil {
		r.failItem(it, "译本章节已删除")
		return nil
	}
	hash := sourceHash(&src)
	r.db().Model(it).Updates(map[string]any{"status": itemRunning, "error": ""})
	r.publishItem(it.ID)
	r.run.begin(it.ID)

	var total usageTotals
	// 同步时原文标题若有修改，重新翻译标题
	title := dst.Title
	if r.job.Mode == modeSync && it.TargetTitle == "" {
		out, err := r.translateTitles(ctx, []string{src.Title}, &total)
		if err != nil {
			r.addJobUsage(total)
			return err
		}
		title = truncate(strings.TrimSpace(out[0]), 255)
	}
	system := contentSystemPrompt(r.job, r.src.Title, src.Title, r.terms)
	var parts []string
	for _, seg := range splitSegments(src.Content, segmentRunes) {
		if seg.code {
			parts = append(parts, seg.text)
			r.run.append(seg.text + "\n\n")
			continue
		}
		out, err := r.translate(ctx, system, seg.text, r.run.append, &total)
		if err != nil {
			r.addJobUsage(total)
			return err
		}
		parts = append(parts, out)
		r.run.append("\n\n")
	}
	content := strings.Join(parts, "\n\n")
	if content != "" {
		content += "\n"
	}
	dst.Title, dst.Content = title, content
	dst.Icon = r.b.core.ExtractDocIcon(content)
	err := r.db().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Document{}).Where("id = ?", dst.ID).Updates(map[string]any{"title": dst.Title, "content": dst.Content, "icon": dst.Icon}).Error; err != nil {
			return err
		}
		rev := r.b.core.NewDocumentRevision(&dst, r.user.ID, "ai_translate")
		return tx.Create(&rev).Error
	})
	if err != nil {
		r.addJobUsage(total)
		r.failItem(it, "保存译文失败")
		return nil
	}
	if dst.Status == "published" {
		r.b.core.GuardDocumentPublish(r.dst, &dst, r.user.ID)
	}
	r.db().Model(&TranslatedDoc{}).Where("target_book_id = ? AND source_doc_id = ?", r.dst.ID, src.ID).
		Updates(map[string]any{"target_doc_id": dst.ID, "source_hash": hash})
	r.db().Model(it).Updates(map[string]any{"status": itemDone, "target_title": dst.Title, "chars": total.chars, "input_tokens": total.in,
		"output_tokens": total.out, "duration_ms": time.Since(started).Milliseconds()})
	r.db().Model(&TranslateJob{}).Where("id = ?", r.job.ID).Update("done", gorm.Expr("done + 1"))
	r.publishItem(it.ID)
	r.addJobUsage(total)
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// startJob 在后台执行任务（已在进行中则忽略）。
func (b *behavior) startJob(job TranslateJob) {
	ctx, cancel := context.WithCancel(ai.WithCaller(context.Background(),
		ai.Caller{UserID: job.UserID, Feature: featureBookTranslate, RefType: "book", RefID: job.TargetBookID, TraceID: job.TraceID}))
	run := &jobRun{id: job.ID, cancel: cancel}
	if !runningJobs.Add(job.ID, run) {
		cancel()
		return
	}
	b.core.Gorm().Model(&TranslateJob{}).Where("id = ?", job.ID).Update("runner", cluster.Self())
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				b.stopJob(job.ID, jobFailed, "翻译出错，请继续或重试")
			}
			runningJobs.Remove(job.ID)
			cancel()
		}()
		b.runJob(ctx, job.ID, run)
	}()
}

func (b *behavior) runJob(ctx context.Context, jobID uint, run *jobRun) {
	db := b.core.Gorm()
	var job TranslateJob
	if db.First(&job, jobID).Error != nil {
		return
	}
	r := &runner{b: b, job: &job, run: run}
	publishUserTask(b.core, &job) // 开始或继续
	var user models.User
	var src, dst models.Book
	if db.First(&user, job.UserID).Error != nil || db.First(&src, job.SourceBookID).Error != nil || db.First(&dst, job.TargetBookID).Error != nil {
		b.stopJob(job.ID, jobFailed, "原书或译本已删除")
		return
	}
	r.user, r.src, r.dst = &user, &src, &dst
	db.Where("book_id = ? AND target_lang = ?", src.ID, job.TargetLang).Order("id ASC").Find(&r.terms)

	if job.Stage == stageOutline {
		if err := r.outline(ctx); err != nil {
			b.interrupted(r, err)
			return
		}
	}
	for {
		var it TranslateItem
		if db.Where("job_id = ? AND status = ? AND target_doc_id <> 0", job.ID, itemPending).Order("ord ASC").First(&it).Error != nil {
			break
		}
		if err := r.chapter(ctx, &it); err != nil {
			db.Model(&it).Updates(map[string]any{"status": itemPending})
			r.publishItem(it.ID)
			if errors.Is(err, context.Canceled) || errors.Is(err, errCharsExhausted) || errors.Is(err, ai.ErrQuotaExceeded) {
				b.interrupted(r, err)
				return
			}
			// 其他错误（如 AI 服务暂时不可用）：该章记为失败，继续下一章
			r.failItem(&it, "AI 服务暂时不可用，可稍后重试")
		}
	}
	b.stopJob(job.ID, jobDone, "")
	db.First(&dst, dst.ID) // 目录阶段可能已把书名换成译名
	var final TranslateJob
	if db.First(&final, job.ID).Error == nil {
		b.core.NotifyI18n(final.UserID, "system", "notify.bookTranslate.done",
			map[string]string{"book": dst.Title, "done": fmt.Sprint(final.Done), "failed": fmt.Sprint(final.Failed)},
			map[string]any{"link": fmt.Sprintf("/book/settings/%s/ai-translate?job=%d", src.Slug, final.ID)})
	}
}

// interrupted 任务被暂停（作者暂停、额度不足）：进行中的章节回到待翻译，可继续。
func (b *behavior) interrupted(r *runner, err error) {
	msg := ""
	switch {
	case errors.Is(err, context.Canceled):
	case errors.Is(err, errCharsExhausted), errors.Is(err, ai.ErrQuotaExceeded):
		msg = err.Error()
	default:
		msg = "AI 服务暂时不可用，可稍后继续"
	}
	b.core.Gorm().Model(&TranslateItem{}).Where("job_id = ? AND status = ?", r.job.ID, itemRunning).Update("status", itemPending)
	b.stopJob(r.job.ID, jobPaused, msg)
}

// stopJob 结束或暂停任务并推送最终状态。
func (b *behavior) stopJob(id uint, status, errMsg string) {
	db := b.core.Gorm()
	updates := map[string]any{"status": status, "error": errMsg}
	if status == jobDone || status == jobFailed {
		now := time.Now()
		updates["finished_at"] = &now
		updates["stage"] = stageDone
	}
	db.Model(&TranslateJob{}).Where("id = ?", id).Updates(updates)
	var job TranslateJob
	if db.First(&job, id).Error == nil {
		var items []TranslateItem
		db.Where("job_id = ?", id).Order("ord ASC").Find(&items)
		jobsHub.Publish(id, "done", jobView{Job: job, Items: items})
		publishUserTask(b.core, &job)
	}
}

// sweepInterruptedJobs 把执行实例已下线的「进行中」任务标记为已暂停（服务重启等；其他在线实例上运行的不受影响），可继续。
func sweepInterruptedJobs(core plugincore.Core) {
	db := core.Gorm()
	if !db.Migrator().HasTable(&TranslateJob{}) {
		return
	}
	var rows []TranslateJob
	db.Select("id, runner").Where("status = ? AND created_at < ?", jobRunning, time.Now().Add(-30*time.Second)).Find(&rows)
	for _, r := range rows {
		if id := r.ID; runningJobs.Orphaned(id, r.Runner) {
			db.Model(&TranslateItem{}).Where("job_id = ? AND status = ?", id, itemRunning).Update("status", itemPending)
			db.Model(&TranslateJob{}).Where("id = ? AND status = ?", id, jobRunning).
				Updates(map[string]any{"status": jobPaused, "error": "服务重启，翻译已暂停，可继续"})
			var job TranslateJob
			if db.First(&job, id).Error == nil {
				publishUserTask(core, &job)
			}
		}
	}
}
