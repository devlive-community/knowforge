package growth

import (
	"strconv"

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

// —— 经验加成：会员方案或等级可给出 growth.xp_bonus（百分比），按规则获得的经验按比例放大 ——

const (
	entXPBonus     = "growth.xp_bonus"
	cfgXPBonusBase = "entitlement_growth_xp_bonus"
	maxXPBonus     = 300
)

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entXPBonus, Kind: plugincore.EntitlementLimit, Unit: "percent", Min: 0, Max: maxXPBonus, Order: 90,
		// 管理员「不受限制」不等于取最大加成：按来源与基础值计算
		AdminUsesBase: true,
		Available:     func(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyGrowth) },
		Base: func(core plugincore.Core) int64 {
			if v, err := strconv.ParseInt(core.GetSetting(cfgXPBonusBase), 10, 64); err == nil && v >= 0 && v <= maxXPBonus {
				return v
			}
			return 0
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgXPBonusBase, strconv.FormatInt(v, 10), "权益：经验加成百分比（基础）")
		},
	})
}

// xpBonus 用户当前的经验加成百分比（0 为无加成）。
func (b *behavior) xpBonus(userID uint) int64 {
	var u models.User
	if b.core.Gorm().First(&u, userID).Error != nil {
		return 0
	}
	return plugincore.EntitlementValue(b.core, &u, entXPBonus)
}
