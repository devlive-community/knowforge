package app

import (
	"testing"

	"knowforge/server/internal/plugins"
)

// 插件自注册：各插件在自己的 plugin_<key>.go 的 init() 中注册，核心不再硬编码清单；
// 校验全部内置插件均已注册且按 Order 稳定排序。
func TestPluginSelfRegistration(t *testing.T) {
	want := []string{
		pluginPDFExport, pluginAchievements, pluginBookTranslations, pluginBookVersions,
		pluginWatermark, pluginBookFollow, pluginGrowth, pluginContentCollect, pluginTags, plugins.KeyMembership, plugins.KeyPayment, plugins.KeyPaidContent, plugins.KeyModeration, plugins.KeyQA, plugins.KeyAIWriter, plugins.KeyChapterGuide, plugins.KeyWebhooks,
	}
	if len(pluginRegistry) != len(want) {
		t.Fatalf("插件数量应为 %d，实际 %d", len(want), len(pluginRegistry))
	}
	for i, key := range want {
		if pluginRegistry[i].Key != key {
			t.Fatalf("顺序[%d] 应为 %q，实际 %q", i, key, pluginRegistry[i].Key)
		}
		if pluginInfoByKey(key) == nil {
			t.Fatalf("插件 %q 未注册", key)
		}
	}
}
