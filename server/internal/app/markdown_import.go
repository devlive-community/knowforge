package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"knowforge/server/internal/models"
)

// Markdown 导入（Issue #87）：把任意 Markdown 文件/目录导入为章节。
//   - 可上传多个 .md 文件，或一个 Markdown 目录的 ZIP（非 KnowForge 导出格式，即不含 book.md）；
//   - 目录 → 父章节（目录下的 README.md / index.md 作为该章节正文），文件 → 章节，按名称自然排序（支持 01- 前缀）；
//   - 标题取 front-matter title，其次首行一级标题（从正文移除），再次文件名；
//   - 相对路径图片上传到当前存储驱动并改写引用；指向包内其他 .md 的链接改写为阅读页链接。

const (
	mdImportMaxFiles = 500
	mdImportMaxFile  = 5 << 20
)

var (
	mdImageRefPattern = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+["'][^"']*["'])?\s*\)`)
	mdHTMLImgPattern  = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']`)
	mdLinkPattern     = regexp.MustCompile(`(^|[^!])\[([^\]]*)\]\(\s*<?([^)\s>]+)>?\s*\)`)
	mdNumberPrefix    = regexp.MustCompile(`^\d+(?:[._\-\s]+|$)`)
	mdIndexNames      = map[string]bool{"readme": true, "index": true, "_index": true}
)

func isMarkdownName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

// skipImportPath 隐藏文件与 macOS 打包残留不导入。
func skipImportPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "__MACOSX" {
			return true
		}
	}
	return false
}

// humanizeName 文件/目录名 → 章节标题：去扩展名与序号前缀，-/_ 换成空格。
func humanizeName(name string) string {
	base := strings.TrimSuffix(name, path.Ext(name))
	title := strings.TrimSpace(mdNumberPrefix.ReplaceAllString(base, ""))
	title = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(title))
	if title == "" {
		return base
	}
	return title
}

// naturalLess 自然排序：数字段按数值比较（2 < 10）。
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, rb := []rune(a)[0], []rune(b)[0]
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, restA := leadingNumber(a)
			nb, restB := leadingNumber(b)
			if na != nb {
				return na < nb
			}
			a, b = restA, restB
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[len(string(ra)):], b[len(string(rb)):]
	}
	return len(a) < len(b)
}

func leadingNumber(s string) (int, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:min(i, 9)])
	return n, s[i:]
}

// parseMarkdownDoc 解析单个 Markdown：front-matter title → 首行一级标题（从正文移除）→ 文件名。
func parseMarkdownDoc(name string, data []byte) (title, body, status string) {
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")
	fields, _, body := parseFrontMatter(text)
	title = strings.TrimSpace(fields.get("title"))
	status = fields.get("status")
	if title == "" {
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
				body = strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n")
			}
			break
		}
	}
	if title == "" {
		title = humanizeName(path.Base(name))
	}
	return truncateText(title, 255), body, status
}

// mdNode 导入树节点：目录或文件。
type mdNode struct {
	name     string // 用于排序的原始名称
	title    string
	body     string
	src      string // 来源 Markdown 路径（目录取其 README/index），用于改写包内链接与解析相对图片
	status   string
	slug     string
	children []*mdNode
}

// stripCommonRoot 所有文件都在同一个顶层目录下时去掉该目录（常见的「整个文件夹打包」），返回目录名。
func stripCommonRoot(files map[string][]byte) (map[string][]byte, string) {
	root := ""
	for p := range files {
		first, rest, found := strings.Cut(p, "/")
		if !found || rest == "" {
			return files, ""
		}
		if root == "" {
			root = first
		} else if root != first {
			return files, ""
		}
	}
	if root == "" {
		return files, ""
	}
	out := make(map[string][]byte, len(files))
	for p, data := range files {
		out[strings.TrimPrefix(p, root+"/")] = data
	}
	return out, root
}

// buildMarkdownTree 由包内文件构造章节树（根节点本身不是章节）。
func buildMarkdownTree(files map[string][]byte) *mdNode {
	root := &mdNode{}
	dirs := map[string]*mdNode{"": root}
	var ensureDir func(dir string) *mdNode
	ensureDir = func(dir string) *mdNode {
		if n, ok := dirs[dir]; ok {
			return n
		}
		parent := ensureDir(parentDir(dir))
		n := &mdNode{name: path.Base(dir), title: truncateText(humanizeName(path.Base(dir)), 255)}
		parent.children = append(parent.children, n)
		dirs[dir] = n
		return n
	}
	names := make([]string, 0, len(files))
	for p := range files {
		if isMarkdownName(p) && !skipImportPath(p) {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	for _, p := range names {
		dir := parentDir(p)
		base := path.Base(p)
		title, body, status := parseMarkdownDoc(p, files[p])
		stem := strings.ToLower(strings.TrimSuffix(base, path.Ext(base)))
		if dir != "" && mdIndexNames[stem] {
			n := ensureDir(dir)
			if n.src == "" {
				n.body, n.src, n.status = body, p, status
				if title != humanizeName(base) { // README 有自己的标题时优先用它
					n.title = title
				}
				continue
			}
		}
		parent := ensureDir(dir)
		name := base
		if dir == "" && mdIndexNames[stem] {
			name = "" // 根目录 README 排在最前
		}
		parent.children = append(parent.children, &mdNode{name: name, title: title, body: body, src: p, status: status})
	}
	var sortTree func(n *mdNode)
	sortTree = func(n *mdNode) {
		sort.SliceStable(n.children, func(i, j int) bool { return naturalLess(n.children[i].name, n.children[j].name) })
		for _, c := range n.children {
			sortTree(c)
		}
	}
	sortTree(root)
	return root
}

func parentDir(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// resolveRelative 解析 Markdown 中的相对引用（去掉查询串与锚点、URL 解码）；外链/绝对路径/锚点返回 false。
func resolveRelative(fromFile, ref string) (string, string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "data:") || strings.Contains(ref, "://") || strings.HasPrefix(ref, "mailto:") {
		return "", "", false
	}
	target, frag, _ := strings.Cut(ref, "#")
	target, _, _ = strings.Cut(target, "?")
	if decoded, err := url.PathUnescape(target); err == nil {
		target = decoded
	}
	resolved := path.Clean(path.Join(parentDir(fromFile), target))
	if strings.HasPrefix(resolved, "../") || resolved == ".." {
		return "", "", false
	}
	return resolved, frag, true
}

// markdownImporter 一次导入的上下文：上传过的图片按包内路径去重。
type markdownImporter struct {
	a        *App
	owner    *models.User // 图片记入其个人存储
	files    map[string][]byte
	maxImage int64
	uploaded map[string]string
	slugs    map[string]string // 来源 Markdown 路径 → 章节 slug
	bookSlug string
}

func (m *markdownImporter) rewriteImages(src, body string) string {
	if src == "" {
		return body
	}
	allowed := m.a.uploadAllowedExts()
	replace := func(match, ref string) string {
		resolved, _, ok := resolveRelative(src, ref)
		if !ok {
			return match
		}
		if url, done := m.uploaded[resolved]; done {
			return strings.Replace(match, ref, url, 1)
		}
		data, found := m.files[resolved]
		ext := strings.ToLower(path.Ext(resolved))
		if !found || !allowed[ext] || int64(len(data)) > m.maxImage {
			return match
		}
		url, err := m.a.storeUserFile(m.owner, "markdown_import", ext, data)
		if err != nil { // 保存失败（含个人存储空间不足）时保留原引用
			return match
		}
		m.uploaded[resolved] = url
		return strings.Replace(match, ref, url, 1)
	}
	body = mdImageRefPattern.ReplaceAllStringFunc(body, func(match string) string {
		return replace(match, mdImageRefPattern.FindStringSubmatch(match)[1])
	})
	return mdHTMLImgPattern.ReplaceAllStringFunc(body, func(match string) string {
		return replace(match, mdHTMLImgPattern.FindStringSubmatch(match)[1])
	})
}

func (m *markdownImporter) rewriteLinks(src, body string) string {
	if src == "" || m.bookSlug == "" {
		return body
	}
	return mdLinkPattern.ReplaceAllStringFunc(body, func(match string) string {
		sub := mdLinkPattern.FindStringSubmatch(match)
		resolved, frag, ok := resolveRelative(src, sub[3])
		if !ok || !isMarkdownName(resolved) {
			return match
		}
		slug, found := m.slugs[resolved]
		if !found {
			return match
		}
		target := "/book/reader/" + m.bookSlug + "/" + slug
		if frag != "" {
			target += "#" + frag
		}
		return sub[1] + "[" + sub[2] + "](" + target + ")"
	})
}

// countNodes 章节数（不含根）。
func countNodes(n *mdNode) int {
	total := 0
	for _, c := range n.children {
		total += 1 + countNodes(c)
	}
	return total
}

// importTree 在书中按树创建章节（parentID 为挂载点，nil 为第一级）；status 为空时按书籍默认规则。
func (m *markdownImporter) importTree(book *models.Book, u *models.User, root *mdNode, parentID *uint, status string) (int, error) {
	// 先为全部节点分配唯一 slug，便于改写包内互相引用的链接
	used := map[string]bool{}
	var assign func(n *mdNode)
	assign = func(n *mdNode) {
		for _, c := range n.children {
			base := slugify(c.title)
			if base == "" {
				base = slugify(humanizeName(c.name))
			}
			if base == "" {
				base = randomSlug("doc")
			}
			slug := m.a.uniqueDocSlug(book.ID, base)
			for i := 2; used[slug]; i++ {
				slug = m.a.uniqueDocSlug(book.ID, fmt.Sprintf("%s-%d", base, i))
			}
			used[slug] = true
			c.slug = slug
			if c.src != "" {
				m.slugs[c.src] = slug
			}
			assign(c)
		}
	}
	assign(root)

	var startOrder int64
	q := m.a.DB.Model(&models.Document{}).Where("book_id = ?", book.ID)
	if parentID != nil {
		q = q.Where("parent_id = ?", *parentID)
	} else {
		q = q.Where("parent_id IS NULL")
	}
	q.Count(&startOrder)

	created := 0
	var toPublish []*models.Document // 请求发布的章节：先以草稿写入，事务结束后交发布守卫审查再发布
	allowComments := true
	var create func(tx *gorm.DB, n *mdNode, parent *uint, order int) error
	create = func(tx *gorm.DB, n *mdNode, parent *uint, order int) error {
		docStatus := status
		if n.status == "published" || n.status == "draft" || n.status == "archived" {
			docStatus = n.status
		}
		if docStatus == "" {
			docStatus = m.a.initialChapterStatus(book, parent)
		}
		content := m.rewriteLinks(n.src, m.rewriteImages(n.src, n.body))
		publish := docStatus == "published"
		if publish {
			docStatus = "draft"
		}
		doc := models.Document{
			BookID: book.ID, ParentID: parent, Title: n.title, Slug: n.slug, Content: content,
			UserID: u.ID, SortOrder: order, Status: docStatus, AllowComments: &allowComments,
		}
		doc.Icon = extractDocIcon(content)
		if err := tx.Create(&doc).Error; err != nil {
			return fmt.Errorf("创建章节「%s」失败: %w", n.title, err)
		}
		revision := newDocumentRevision(&doc, u.ID, "create")
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		created++
		if publish {
			toPublish = append(toPublish, &doc)
		}
		for i, c := range n.children {
			if err := create(tx, c, &doc.ID, i); err != nil {
				return err
			}
		}
		return nil
	}
	err := m.a.DB.Transaction(func(tx *gorm.DB) error {
		for i, c := range root.children {
			if err := create(tx, c, parentID, int(startOrder)+i); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		for _, doc := range toPublish {
			if m.a.tryPublishDocument(book, doc, u.ID) == "" && doc.Status == "published" {
				m.a.notifyDocumentIndexableChange(nil, book, nil, doc)
			}
		}
	}
	return created, err
}

// readMarkdownUpload 读取上传的 Markdown：多个 .md（可附带图片）或一个 ZIP；返回包内文件与建议书名。
func readMarkdownUpload(c *gin.Context) (map[string][]byte, string, error) {
	form, err := c.MultipartForm()
	if err != nil || form == nil || len(form.File["files"]) == 0 {
		return nil, "", fmt.Errorf("请选择要导入的 Markdown 文件")
	}
	headers := form.File["files"]
	files := map[string][]byte{}
	total := int64(0)
	if len(headers) == 1 && strings.EqualFold(path.Ext(headers[0].Filename), ".zip") {
		h := headers[0]
		if h.Size <= 0 || h.Size > importMaxTotalBytes {
			return nil, "", fmt.Errorf("ZIP 文件必须小于 64MB")
		}
		f, err := h.Open()
		if err != nil {
			return nil, "", fmt.Errorf("读取 ZIP 文件失败")
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, importMaxTotalBytes+1))
		if err != nil {
			return nil, "", fmt.Errorf("读取 ZIP 文件失败")
		}
		entries, err := readSafeZip(raw)
		if err != nil {
			return nil, "", err
		}
		return entries, humanizeName(h.Filename), nil
	}
	if len(headers) > mdImportMaxFiles {
		return nil, "", fmt.Errorf("一次最多导入 %d 个文件", mdImportMaxFiles)
	}
	for _, h := range headers {
		name := path.Base(strings.ReplaceAll(h.Filename, "\\", "/"))
		if name == "" || name == "." || skipImportPath(name) {
			continue
		}
		if isMarkdownName(name) && h.Size > mdImportMaxFile {
			return nil, "", fmt.Errorf("%s 超过 5MB", name)
		}
		total += h.Size
		if total > importMaxTotalBytes {
			return nil, "", fmt.Errorf("上传文件总大小不能超过 64MB")
		}
		f, err := h.Open()
		if err != nil {
			return nil, "", fmt.Errorf("读取 %s 失败", name)
		}
		data, err := io.ReadAll(io.LimitReader(f, importMaxTotalBytes))
		f.Close()
		if err != nil {
			return nil, "", fmt.Errorf("读取 %s 失败", name)
		}
		files[name] = data
	}
	return files, "", nil
}

// readSafeZip 解压 ZIP（拒绝越界路径，限制文件数与解压体量）。
func readSafeZip(raw []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("无法解析 ZIP 文件")
	}
	if len(reader.File) > importMaxEntries {
		return nil, fmt.Errorf("ZIP 内文件数量过多")
	}
	entries := map[string][]byte{}
	total := 0
	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			return nil, fmt.Errorf("ZIP 内存在非法路径: %s", name)
		}
		if entry.FileInfo().IsDir() || skipImportPath(name) {
			continue
		}
		data, err := readZipEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %v", name, err)
		}
		total += len(data)
		if total > importMaxTotalBytes {
			return nil, fmt.Errorf("ZIP 解压后体量过大")
		}
		entries[name] = data
	}
	return entries, nil
}

// importMarkdownAsBook 由 Markdown 文件创建新书（书与章节默认草稿；publish 为 true 时章节直接发布）。
func (a *App) importMarkdownAsBook(files map[string][]byte, u *models.User, title, fallbackTitle string, publish bool) (zipImportResult, int, error) {
	files, rootDir := stripCommonRoot(files)
	tree := buildMarkdownTree(files)
	count := countNodes(tree)
	if count == 0 {
		return zipImportResult{}, http.StatusBadRequest, fmt.Errorf("没有找到 Markdown 文件（.md / .markdown）")
	}
	if count > maxImportedChapters {
		return zipImportResult{}, http.StatusBadRequest, fmt.Errorf("章节数量过多（最多 %d 个）", maxImportedChapters)
	}
	if err := a.EnsureBookQuota(u); err != nil {
		return zipImportResult{}, http.StatusForbidden, err
	}
	title = strings.TrimSpace(title)
	for _, candidate := range []string{humanizeName(rootDir), fallbackTitle} {
		if title == "" && strings.TrimSpace(candidate) != "" {
			title = candidate
		}
	}
	if title == "" && len(tree.children) == 1 {
		title = tree.children[0].title
	}
	if title == "" {
		title = "导入的书籍"
	}
	title = truncateText(title, 255)
	book := models.Book{Title: title, Slug: a.uniqueBookSlug(slugify(title)), UserID: u.ID, Status: "draft", OrderCol: "created_at", OrderDir: "asc"}
	if book.Slug == "" {
		book.Slug = a.uniqueBookSlug(randomSlug("book"))
	}
	if err := a.DB.Create(&book).Error; err != nil {
		return zipImportResult{}, http.StatusInternalServerError, fmt.Errorf("创建书籍失败: %w", err)
	}
	status := "draft"
	if publish {
		status = "published"
	}
	m := &markdownImporter{a: a, owner: u, files: files, maxImage: a.userUploadMaxBytes(u), uploaded: map[string]string{}, slugs: map[string]string{}, bookSlug: book.Slug}
	created, err := m.importTree(&book, u, tree, nil, status)
	if err != nil {
		a.DB.Unscoped().Delete(&book)
		return zipImportResult{}, http.StatusInternalServerError, err
	}
	return zipImportResult{Book: book, ImportedDoc: created, Source: "markdown", Message: fmt.Sprintf("导入完成：《%s》共 %d 个章节", book.Title, created)}, http.StatusOK, nil
}

// ImportMarkdownBook POST /import/markdown（multipart files[]：多个 .md 或一个 ZIP；title?、publish?）由 Markdown 新建书籍。
func (a *App) ImportMarkdownBook(c *gin.Context) {
	u := currentUser(c)
	if a.failBookQuota(c, u) {
		return
	}
	files, fallback, err := readMarkdownUpload(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	result, status, err := a.importMarkdownAsBook(files, u, c.PostForm("title"), fallback, c.PostForm("publish") == "true")
	if err != nil {
		fail(c, status, err.Error())
		return
	}
	ok(c, result)
}

// ImportMarkdownDocuments POST /books/:id/documents/import-markdown（multipart files[]、parent_id?）把 Markdown 导入为本书章节。
func (a *App) ImportMarkdownDocuments(c *gin.Context) {
	book, status := a.findBook(c)
	if book == nil {
		fail(c, status, "书籍不存在")
		return
	}
	u := currentUser(c)
	if !a.canEditBookContent(u, book) {
		fail(c, http.StatusForbidden, "无权编辑该书籍")
		return
	}
	var parentID *uint
	if raw := strings.TrimSpace(c.PostForm("parent_id")); raw != "" && raw != "0" {
		id, err := strconv.ParseUint(raw, 10, 64)
		var parent models.Document
		if err != nil || a.DB.Select("id").Where("id = ? AND book_id = ?", id, book.ID).First(&parent).Error != nil {
			fail(c, http.StatusBadRequest, "父章节不存在")
			return
		}
		pid := uint(id)
		parentID = &pid
	}
	files, _, err := readMarkdownUpload(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	files, _ = stripCommonRoot(files)
	tree := buildMarkdownTree(files)
	count := countNodes(tree)
	if count == 0 {
		fail(c, http.StatusBadRequest, "没有找到 Markdown 文件（.md / .markdown）")
		return
	}
	if count > maxImportedChapters {
		fail(c, http.StatusBadRequest, fmt.Sprintf("章节数量过多（最多 %d 个）", maxImportedChapters))
		return
	}
	m := &markdownImporter{a: a, owner: u, files: files, maxImage: a.userUploadMaxBytes(u), uploaded: map[string]string{}, slugs: map[string]string{}, bookSlug: book.Slug}
	created, err := m.importTree(book, u, tree, parentID, "")
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"imported_doc": created, "message": fmt.Sprintf("已导入 %d 个章节", created)})
}
