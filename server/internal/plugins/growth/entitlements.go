package growth

import (
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 等级特权：每个等级可配置权益（书籍数量、上传大小、采集等），用户获得「当前等级及以下各启用等级」配置的累计结果
// （由低到高依次覆盖，高等级只需配置提升的项）。作为权益来源参与计算：优先级低于会员（会员有效时独占），高于基础值。

const levelEntitlementPriority = 50

func init() {
	plugincore.RegisterEntitlementSource(plugincore.EntitlementSource{
		Key: "level", Priority: levelEntitlementPriority,
		Resolve: func(core plugincore.Core, u *models.User) (map[string]int64, bool) {
			if !core.PluginEnabled(plugins.KeyGrowth) {
				return nil, false
			}
			b := &behavior{core: core}
			current := b.growthProfile(u.ID).CurrentLevel
			var levels []models.LevelDefinition
			core.Gorm().Where("status = ? AND level <= ?", "active", current).Order("level ASC").Find(&levels)
			values := map[string]int64{}
			for _, lvl := range levels {
				for k, v := range lvl.Entitlements {
					values[k] = v
				}
			}
			if len(values) == 0 {
				return nil, false
			}
			return values, false
		},
		// 各启用等级的累计取值（由低到高覆盖，与 Resolve 一致）
		Grants: func(core plugincore.Core, key string) []plugincore.EntitlementGrant {
			if !core.PluginEnabled(plugins.KeyGrowth) {
				return nil
			}
			var levels []models.LevelDefinition
			core.Gorm().Where("status = ?", "active").Order("level ASC").Find(&levels)
			out := []plugincore.EntitlementGrant{}
			var current int64
			set := false
			for _, lvl := range levels {
				if v, ok := lvl.Entitlements[key]; ok {
					current, set = v, true
				}
				if set {
					out = append(out, plugincore.EntitlementGrant{Source: "level", Label: lvl.Name, Rank: lvl.Level, Value: current})
				}
			}
			return out
		},
	})
}
