package contentcollect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"

	"knowforge/server/internal/mdclean"
)

// —— 采集规则：网页正文提取时的清理规则，由管理员在「内容采集 · 采集规则」中配置，对所有用户的采集生效。
// 内置规则（原有的清理行为）可逐项启用/停用；自定义规则支持「删除元素」（转为 Markdown 前按简单选择器移除）、
// 「删除行」与「替换文本」（转为 Markdown 后按正则处理，代码块内的行不受删除行影响），可限定只对某些站点生效。——

const (
	cfgRules       = "collect_rules"
	maxCustomRules = 100
)

// 内置规则键（前端按键显示名称与说明）。
const (
	ruleLayout        = "layout_regions"  // 页头、页脚、导航、侧栏、广告、评论、分享等区域
	rulePageTOC       = "page_toc"        // 页内目录（On this page）
	ruleDecorations   = "doc_decorations" // 文档站装饰：版本标记与旧版本提示、编辑链接、最后更新时间、上一页/下一页
	rulePermalinks    = "permalinks"      // 标题后的永久链接锚点（# ¶ 🔗 等）
	ruleEmptyHeadings = "empty_headings"  // 只剩 # 号的空标题行与孤立的 # ¶ 行
	rulePageFeedback  = "page_feedback"   // 页尾「Was this page helpful?」等反馈提问
)

var builtinRuleKeys = []string{ruleLayout, rulePageTOC, ruleDecorations, rulePermalinks, ruleEmptyHeadings, rulePageFeedback}

// 自定义规则类型。
const (
	customRemoveElement = "remove_element"
	customRemoveLine    = "remove_line"
	customReplace       = "replace"
)

type customRule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Type        string `json:"type"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Hosts       string `json:"hosts"` // 逗号分隔的域名，空为所有站点；子域名也匹配（example.com 匹配 docs.example.com）
}

type collectRules struct {
	Builtin map[string]bool `json:"builtin"`
	Custom  []customRule    `json:"custom"`
}

func (r collectRules) builtinOn(key string) bool {
	v, ok := r.Builtin[key]
	return !ok || v
}

// rules 当前保存的规则（未保存过时内置规则全部启用）。
func (cc *behavior) rules() collectRules {
	r := collectRules{Builtin: map[string]bool{}}
	if raw := cc.core.GetSetting(cfgRules); raw != "" {
		_ = json.Unmarshal([]byte(raw), &r)
	}
	if r.Builtin == nil {
		r.Builtin = map[string]bool{}
	}
	if r.Custom == nil {
		r.Custom = []customRule{}
	}
	return r
}

// ruleSet 针对某个网址编译好的规则。
type ruleSet struct {
	layout, toc, decorations, permalinks, emptyHeadings, feedback bool
	selectors                                                     []elementSelector
	removeLines                                                   []*regexp.Regexp
	replaces                                                      []replaceRule
}

type replaceRule struct {
	re   *regexp.Regexp
	repl string
}

// defaultRules 内置规则全部启用、没有自定义规则。
var defaultRules = &ruleSet{layout: true, toc: true, decorations: true, permalinks: true, emptyHeadings: true, feedback: true}

func hostMatches(hosts string, u *url.URL) bool {
	if strings.TrimSpace(hosts) == "" {
		return true
	}
	if u == nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, part := range strings.Split(hosts, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p != "" && (h == p || strings.HasSuffix(h, "."+p)) {
			return true
		}
	}
	return false
}

// compile 编译出对 u 生效的规则（停用的与不匹配站点的自定义规则跳过；无法编译的规则忽略，保存时已校验）。
func (r collectRules) compile(u *url.URL) *ruleSet {
	rs := &ruleSet{
		layout: r.builtinOn(ruleLayout), toc: r.builtinOn(rulePageTOC), decorations: r.builtinOn(ruleDecorations),
		permalinks: r.builtinOn(rulePermalinks), emptyHeadings: r.builtinOn(ruleEmptyHeadings), feedback: r.builtinOn(rulePageFeedback),
	}
	for _, c := range r.Custom {
		if !c.Enabled || !hostMatches(c.Hosts, u) {
			continue
		}
		switch c.Type {
		case customRemoveElement:
			if sels, err := parseSelectors(c.Pattern); err == nil {
				rs.selectors = append(rs.selectors, sels...)
			}
		case customRemoveLine:
			if re, err := regexp.Compile(c.Pattern); err == nil {
				rs.removeLines = append(rs.removeLines, re)
			}
		case customReplace:
			if re, err := regexp.Compile(c.Pattern); err == nil {
				rs.replaces = append(rs.replaces, replaceRule{re: re, repl: c.Replacement})
			}
		}
	}
	return rs
}

// —— 简单选择器：tag、#id、.class、[attr]、[attr=value] 的组合（如 div.feedback、a[aria-label=Edit]），逗号分隔多个；
// 不支持后代/子代关系。——

type attrMatch struct {
	name, value string
	hasValue    bool
}

type elementSelector struct {
	tag     string
	id      string
	classes []string
	attrs   []attrMatch
}

var selectorPart = regexp.MustCompile(`^(?:[a-zA-Z][a-zA-Z0-9-]*|\*)?(?:#[\w-]+|\.[\w-]+|\[[\w:-]+(?:=(?:"[^"]*"|'[^']*'|[^\]]*))?\])*$`)
var selectorToken = regexp.MustCompile(`#[\w-]+|\.[\w-]+|\[[\w:-]+(?:=(?:"[^"]*"|'[^']*'|[^\]]*))?\]`)

func parseSelectors(raw string) ([]elementSelector, error) {
	var out []elementSelector
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if !selectorPart.MatchString(p) {
			return nil, fmt.Errorf("不支持的选择器「%s」：只支持 tag、#id、.class、[attr]、[attr=value] 的组合，不支持空格分隔的层级", p)
		}
		sel := elementSelector{}
		rest := p
		if i := strings.IndexAny(p, "#.["); i != 0 {
			if i < 0 {
				i = len(p)
			}
			if tag := p[:i]; tag != "*" {
				sel.tag = strings.ToLower(tag)
			}
			rest = p[i:]
		}
		for _, tok := range selectorToken.FindAllString(rest, -1) {
			switch tok[0] {
			case '#':
				sel.id = tok[1:]
			case '.':
				sel.classes = append(sel.classes, tok[1:])
			case '[':
				body := tok[1 : len(tok)-1]
				name, value, has := strings.Cut(body, "=")
				value = strings.Trim(value, `"'`)
				sel.attrs = append(sel.attrs, attrMatch{name: strings.ToLower(name), value: value, hasValue: has})
			}
		}
		if sel.tag == "" && sel.id == "" && len(sel.classes) == 0 && len(sel.attrs) == 0 {
			return nil, fmt.Errorf("选择器「%s」不能匹配所有元素", p)
		}
		out = append(out, sel)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("请填写选择器")
	}
	return out, nil
}

func (s elementSelector) matches(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if s.tag != "" && !strings.EqualFold(n.Data, s.tag) {
		return false
	}
	if s.id != "" && attribute(n, "id") != s.id {
		return false
	}
	if len(s.classes) > 0 {
		have := map[string]bool{}
		for _, c := range strings.Fields(attribute(n, "class")) {
			have[c] = true
		}
		for _, c := range s.classes {
			if !have[c] {
				return false
			}
		}
	}
	for _, a := range s.attrs {
		if !hasAttribute(n, a.name) || (a.hasValue && attribute(n, a.name) != a.value) {
			return false
		}
	}
	return true
}

func (rs *ruleSet) removesElement(n *html.Node) bool {
	for _, s := range rs.selectors {
		if s.matches(n) {
			return true
		}
	}
	return false
}

// —— Markdown 后处理 ——

var (
	emptyHeadingLine = regexp.MustCompile(`^\s{0,3}#{1,6}\s*$`)
	glyphOnlyLine    = regexp.MustCompile(`^\s*[#¶§🔗]\s*$`)
	fenceLine        = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
)

// postProcess 转为 Markdown 后的清理（顺序：页尾反馈 → 永久链接锚点 → 空标题 → 自定义删除行 → 自定义替换）。
func (rs *ruleSet) postProcess(md string) string {
	if rs.feedback {
		md = trimPageFeedback(md)
	}
	if rs.permalinks {
		md = mdclean.StripPermalinkAnchors(md)
	}
	if rs.emptyHeadings || len(rs.removeLines) > 0 {
		lines := strings.Split(md, "\n")
		out := lines[:0]
		fence := ""
		for _, line := range lines {
			if f := fenceLine.FindStringSubmatch(line); f != nil {
				if fence == "" {
					fence = f[1][:1]
				} else if strings.HasPrefix(f[1], fence) {
					fence = ""
				}
				out = append(out, line)
				continue
			}
			if fence == "" {
				if rs.emptyHeadings && (emptyHeadingLine.MatchString(line) || glyphOnlyLine.MatchString(line)) {
					continue
				}
				if matchesAny(rs.removeLines, line) {
					continue
				}
			}
			out = append(out, line)
		}
		md = strings.Join(out, "\n")
	}
	for _, r := range rs.replaces {
		md = r.re.ReplaceAllString(md, r.repl)
	}
	return collapseBlankLines(strings.TrimSpace(md))
}

func matchesAny(res []*regexp.Regexp, line string) bool {
	for _, re := range res {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

var extraBlankLines = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(md string) string { return extraBlankLines.ReplaceAllString(md, "\n\n") }

// —— 管理接口 ——

type builtinRuleView struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

func (r collectRules) view() gin.H {
	builtin := make([]builtinRuleView, 0, len(builtinRuleKeys))
	for _, k := range builtinRuleKeys {
		builtin = append(builtin, builtinRuleView{Key: k, Enabled: r.builtinOn(k)})
	}
	return gin.H{"builtin": builtin, "custom": r.Custom}
}

// validate 校验并规整规则；返回可展示的错误。
func (r *collectRules) validate() error {
	builtin := map[string]bool{}
	for _, k := range builtinRuleKeys {
		builtin[k] = r.builtinOn(k)
	}
	r.Builtin = builtin
	if len(r.Custom) > maxCustomRules {
		return fmt.Errorf("自定义规则最多 %d 条", maxCustomRules)
	}
	for i := range r.Custom {
		c := &r.Custom[i]
		c.Name = strings.TrimSpace(c.Name)
		c.Pattern = strings.TrimSpace(c.Pattern)
		c.Hosts = strings.TrimSpace(c.Hosts)
		if c.ID == "" {
			c.ID = fmt.Sprintf("r%d", time.Now().UnixNano()+int64(i))
		}
		label := c.Name
		if label == "" {
			label = fmt.Sprintf("第 %d 条", i+1)
		}
		if utf8.RuneCountInString(c.Name) > 60 || len(c.Pattern) > 500 || len(c.Replacement) > 500 || len(c.Hosts) > 500 {
			return fmt.Errorf("规则「%s」过长", label)
		}
		if c.Pattern == "" {
			return fmt.Errorf("规则「%s」缺少匹配内容", label)
		}
		switch c.Type {
		case customRemoveElement:
			if _, err := parseSelectors(c.Pattern); err != nil {
				return fmt.Errorf("规则「%s」：%v", label, err)
			}
		case customRemoveLine, customReplace:
			if _, err := regexp.Compile(c.Pattern); err != nil {
				return fmt.Errorf("规则「%s」的正则表达式有误：%v", label, err)
			}
		default:
			return fmt.Errorf("规则「%s」的类型无效", label)
		}
	}
	return nil
}

// AdminGetRules GET /admin/collect/rules 采集规则（内置规则的启用状态与自定义规则）。
func (cc *behavior) AdminGetRules(c *gin.Context) {
	cc.core.OK(c, cc.rules().view())
}

type rulesRequest struct {
	Builtin []builtinRuleView `json:"builtin"`
	Custom  []customRule      `json:"custom"`
}

func (req rulesRequest) toRules() collectRules {
	r := collectRules{Builtin: map[string]bool{}, Custom: req.Custom}
	for _, b := range req.Builtin {
		r.Builtin[b.Key] = b.Enabled
	}
	if r.Custom == nil {
		r.Custom = []customRule{}
	}
	return r
}

// AdminUpdateRules PUT /admin/collect/rules {builtin:[{key,enabled}], custom:[…]} 保存采集规则。
func (cc *behavior) AdminUpdateRules(c *gin.Context) {
	var req rulesRequest
	if c.ShouldBindJSON(&req) != nil {
		cc.core.Fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	r := req.toRules()
	if err := r.validate(); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(r)
	if err := cc.core.SetSetting(cfgRules, string(raw), "内容采集：采集规则（内置规则开关与自定义规则）"); err != nil {
		cc.core.Fail(c, http.StatusInternalServerError, "保存失败")
		return
	}
	cc.core.RecordAudit(c, "collect.rules_updated", "config", cfgRules, "采集规则", map[string]any{"custom": len(r.Custom)})
	cc.core.OK(c, r.view())
}

// AdminPreviewRules POST /admin/collect/rules/preview {url, render_mode, builtin, custom} 用（未保存的）规则试采一个网页，返回 Markdown。
func (cc *behavior) AdminPreviewRules(c *gin.Context) {
	var req struct {
		rulesRequest
		URL        string `json:"url"`
		RenderMode string `json:"render_mode"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.URL) == "" {
		cc.core.Fail(c, http.StatusBadRequest, "请填写网页地址")
		return
	}
	r := req.toRules()
	if err := r.validate(); err != nil {
		cc.core.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	article, _, mode, err := cc.collectWebArticleWithRules(ctx, webImportPayload{URL: req.URL, RenderMode: req.RenderMode}, &r)
	if err != nil {
		cc.failWebImport(c, err)
		return
	}
	cc.core.OK(c, gin.H{"title": article.Title, "markdown": article.Markdown, "render_mode": mode})
}
