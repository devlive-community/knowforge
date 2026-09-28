package app

import (
	"regexp"
	"strings"
	"testing"

	"knowforge/server/internal/authz"
	"knowforge/server/internal/plugins"
)

var hyphenWord = regexp.MustCompile(`-(\w)`)

// 访问令牌可选的每项权限（普通用户的核心权限与全部插件的用户权限）都要有「资源 · 操作」的中英文文案
// （account.tokens.resource.* / account.tokens.action.*，资源名中的连字符转为驼峰），否则创建令牌时会显示原始键。
func TestAccessTokenPermissionsAreTranslated(t *testing.T) {
	dicts := map[string]map[string]string{"zh": loadWebLocale(t, "zh.ts"), "en": loadWebLocale(t, "en.ts")}
	perms := authz.ForRole("user")
	for _, m := range plugins.All() {
		perms = append(perms, m.UserPerms...)
	}
	checked := 0
	for _, p := range perms {
		if strings.HasPrefix(string(p), "auth:") {
			continue
		}
		parts := strings.SplitN(string(p), ":", 2)
		resource := hyphenWord.ReplaceAllStringFunc(parts[0], func(s string) string { return strings.ToUpper(s[1:]) })
		for _, key := range []string{"account.tokens.resource." + resource, "account.tokens.action." + parts[1]} {
			for lang, dict := range dicts {
				if strings.TrimSpace(dict[key]) == "" {
					t.Errorf("%s 缺少文案 %s（权限 %s）", lang, key, p)
				}
			}
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("应校验全部可选权限，实际 %d 项", checked)
	}
}
