package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"knowforge/server/internal/models"
)

// Node 书籍模板中的一个章节。
type Node struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Children []Node `json:"children,omitempty"`
}

// normalizeTree 校验并规整书籍模板目录：去掉标题首尾空白、限制层级、章节数与正文大小；返回章节总数。
func normalizeTree(nodes []Node) ([]Node, int, error) {
	count, total := 0, 0
	var walk func(list []Node, depth int) ([]Node, error)
	walk = func(list []Node, depth int) ([]Node, error) {
		if depth > maxDepth {
			return nil, fmt.Errorf("目录最多 %d 级", maxDepth)
		}
		out := make([]Node, 0, len(list))
		for _, n := range list {
			n.Title = strings.TrimSpace(n.Title)
			if n.Title == "" {
				return nil, errors.New("章节标题不能为空")
			}
			if utf8.RuneCountInString(n.Title) > 255 {
				return nil, errors.New("章节标题过长")
			}
			if len(n.Content) > maxContentBytes {
				return nil, fmt.Errorf("章节「%s」的正文过长", n.Title)
			}
			count++
			total += len(n.Content)
			if count > maxNodes {
				return nil, fmt.Errorf("模板最多包含 %d 个章节", maxNodes)
			}
			if total > maxTotalBytes {
				return nil, errors.New("模板正文合计过大")
			}
			children, err := walk(n.Children, depth+1)
			if err != nil {
				return nil, err
			}
			n.Children = children
			out = append(out, n)
		}
		return out, nil
	}
	out, err := walk(nodes, 1)
	if err != nil {
		return nil, 0, err
	}
	if count == 0 {
		return nil, 0, errors.New("书籍模板至少需要一个章节")
	}
	return out, count, nil
}

func decodeTree(raw string) []Node {
	var nodes []Node
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &nodes)
	}
	if nodes == nil {
		nodes = []Node{}
	}
	return nodes
}

func encodeTree(nodes []Node) string {
	raw, _ := json.Marshal(nodes)
	return string(raw)
}

// treeFromDocuments 按目录顺序把书中章节整理为模板目录（跳过外链章节及其子章节）；withContent 为 false 时只保留目录结构。
func treeFromDocuments(docs []models.Document, withContent bool) []Node {
	children := map[uint][]models.Document{}
	ids := map[uint]bool{}
	for _, d := range docs {
		ids[d.ID] = true
	}
	var roots []models.Document
	for _, d := range docs {
		if d.ParentID != nil && ids[*d.ParentID] {
			children[*d.ParentID] = append(children[*d.ParentID], d)
		} else if d.ParentID == nil {
			roots = append(roots, d)
		}
	}
	var build func(list []models.Document, depth int) []Node
	build = func(list []models.Document, depth int) []Node {
		out := []Node{}
		for _, d := range list {
			if d.ExternalURL != "" || depth > maxDepth {
				continue
			}
			n := Node{Title: d.Title, Children: build(children[d.ID], depth+1)}
			if withContent {
				n.Content = d.Content
			}
			out = append(out, n)
		}
		return out
	}
	return build(roots, 1)
}

// —— 变量 ——

// Vars 模板变量的取值。
type Vars struct {
	Book    string
	Chapter string
	Author  string
	Now     time.Time
}

// render 把正文中的 {{变量}} 替换为实际值；未知变量原样保留。支持：
// {{date}} 2026-10-03、{{time}} 14:05、{{datetime}}、{{year}}、{{month}}、{{day}}、{{book}} 书名、{{chapter}} 章节名、{{author}} 作者名。
func render(text string, v Vars) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	now := v.Now
	return strings.NewReplacer(
		"{{date}}", now.Format("2006-01-02"),
		"{{time}}", now.Format("15:04"),
		"{{datetime}}", now.Format("2006-01-02 15:04"),
		"{{year}}", now.Format("2006"),
		"{{month}}", now.Format("01"),
		"{{day}}", now.Format("02"),
		"{{book}}", v.Book,
		"{{chapter}}", v.Chapter,
		"{{author}}", v.Author,
	).Replace(text)
}
