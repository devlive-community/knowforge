package plugincore

import (
	"fmt"
	"sort"

	"knowforge/server/internal/models"
)

// —— 权益（Entitlements）：按用户生效的能力上限/开关 ——
//
// 定义：核心与插件登记可配置的权益（数值上限或开关），并提供「基础值」（全站默认，读取现有站点设置，
// 未配置时保持现状不额外限制）。
// 来源：插件登记权益来源（如成长等级、会员方案），按优先级从高到低依次取值：
//   - 每个键取第一个给出该键取值的来源；
//   - 独占来源（exclusive=true，如有效会员）给出结果后不再看更低优先级的来源，未配置的键回退基础值；
//   - 管理员不受限制；功能不可用（如所属插件被禁用）时开关恒为关、上限恒为 0。
// 各功能只需调用 EntitlementValue 取当前用户的值即可，不关心来源。

// Unlimited 数值型权益的「不限」取值。
const Unlimited int64 = -1

// 权益类型。
const (
	EntitlementLimit = "limit" // 数值上限（-1 = 不限，允许时）
	EntitlementFlag  = "flag"  // 开关（1 开 / 0 关）
)

// EntitlementDef 一项可配置的权益。
type EntitlementDef struct {
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	Unit           string `json:"unit"` // 数值单位（前端 i18n：entitlement.unit.<unit>）
	Min            int64  `json:"min"`
	Max            int64  `json:"max"`
	AllowUnlimited bool   `json:"allow_unlimited"`
	Order          int    `json:"-"`
	// Base 基础值（全站默认）；SetBase 保存基础值（通常写回对应的站点设置）。
	Base    func(core Core) int64              `json:"-"`
	SetBase func(core Core, value int64) error `json:"-"`
	// Available 功能当前是否可用（如所属插件已启用）；nil 表示始终可用。
	Available func(core Core) bool `json:"-"`
	// AdminUsesBase 管理员也按来源与基础值计算（用于加成类权益：管理员「不受限制」不等于取最大加成）。
	AdminUsesBase bool `json:"-"`
}

// EntitlementSource 权益来源（如成长等级、会员方案）。
type EntitlementSource struct {
	Key      string
	Priority int
	// Resolve 返回该来源为用户给出的权益取值；exclusive 为 true 时更低优先级的来源不再参与。
	Resolve func(core Core, u *models.User) (values map[string]int64, exclusive bool)
	// Grants 列出该来源中各档（如会员方案、成长等级）为某项权益给出的取值（可选），
	// 供其他插件向用户展示「哪些人能达到」，如作者选择「哪些会员免费阅读」。
	Grants func(core Core, key string) []EntitlementGrant
}

// EntitlementGrant 某个来源中的一档为某项权益给出的取值。
type EntitlementGrant struct {
	Source string `json:"source"` // 来源键（base 表示所有用户的基础值）
	Label  string `json:"label"`  // 展示名（如方案名、等级名）
	Rank   int    `json:"rank"`   // 同一来源内的次序（如成长等级序号），用于「某等级及以上」
	Value  int64  `json:"value"`
}

// EntitlementGrants 某项权益在各来源中的授予情况：基础值（大于 0 时）与各来源登记的档位。
func EntitlementGrants(core Core, key string) []EntitlementGrant {
	out := []EntitlementGrant{}
	if def, ok := EntitlementDefByKey(key); ok && def.Base != nil {
		if v := def.Base(core); v > 0 || v == Unlimited {
			out = append(out, EntitlementGrant{Source: "base", Value: v})
		}
	}
	for _, s := range entitlementSources {
		if s.Grants != nil {
			out = append(out, s.Grants(core, key)...)
		}
	}
	return out
}

// ResolvedEntitlement 用户某项权益的生效值与来源（base | admin | 来源键 | unavailable）。
type ResolvedEntitlement struct {
	Key    string `json:"key"`
	Value  int64  `json:"value"`
	Source string `json:"source"`
}

var (
	entitlementDefs    []EntitlementDef
	entitlementSources []EntitlementSource
)

// RegisterEntitlement 登记一项权益定义。
func RegisterEntitlement(def EntitlementDef) {
	entitlementDefs = append(entitlementDefs, def)
	sort.SliceStable(entitlementDefs, func(i, j int) bool { return entitlementDefs[i].Order < entitlementDefs[j].Order })
}

// Entitlements 返回全部权益定义（按展示顺序）。
func Entitlements() []EntitlementDef { return entitlementDefs }

// EntitlementDefByKey 按键查权益定义。
func EntitlementDefByKey(key string) (EntitlementDef, bool) {
	for _, d := range entitlementDefs {
		if d.Key == key {
			return d, true
		}
	}
	return EntitlementDef{}, false
}

// RegisterEntitlementSource 登记权益来源。
func RegisterEntitlementSource(src EntitlementSource) {
	entitlementSources = append(entitlementSources, src)
	sort.SliceStable(entitlementSources, func(i, j int) bool { return entitlementSources[i].Priority > entitlementSources[j].Priority })
}

// EntitlementSourceKeys 返回已登记的权益来源键（按优先级从高到低）。
func EntitlementSourceKeys() []string {
	keys := make([]string, 0, len(entitlementSources))
	for _, s := range entitlementSources {
		keys = append(keys, s.Key)
	}
	return keys
}

// ValidateEntitlementValue 校验某项权益的配置值（类型与范围）。
func ValidateEntitlementValue(key string, value int64) error {
	def, ok := EntitlementDefByKey(key)
	if !ok {
		return fmt.Errorf("未知的权益：%s", key)
	}
	if def.Kind == EntitlementFlag {
		if value != 0 && value != 1 {
			return fmt.Errorf("权益 %s 只能为开（1）或关（0）", key)
		}
		return nil
	}
	if value == Unlimited && def.AllowUnlimited {
		return nil
	}
	if value < def.Min || value > def.Max {
		return fmt.Errorf("权益 %s 需在 %d 到 %d 之间", key, def.Min, def.Max)
	}
	return nil
}

// ValidateEntitlementMap 校验一组权益配置（来源的配置，如等级/会员方案）。
func ValidateEntitlementMap(values map[string]int64) error {
	for k, v := range values {
		if err := ValidateEntitlementValue(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ResolveEntitlements 计算用户全部权益的生效值（u 为 nil 时即游客：只有基础值）。
func ResolveEntitlements(core Core, u *models.User) map[string]ResolvedEntitlement {
	out := make(map[string]ResolvedEntitlement, len(entitlementDefs))
	isAdmin := u != nil && core.IsAdmin(u)
	for _, def := range entitlementDefs {
		switch {
		case def.Available != nil && !def.Available(core):
			out[def.Key] = ResolvedEntitlement{Key: def.Key, Value: 0, Source: "unavailable"}
		case isAdmin && !def.AdminUsesBase:
			v := int64(1)
			if def.Kind == EntitlementLimit {
				v = def.Max
				if def.AllowUnlimited {
					v = Unlimited
				}
			}
			out[def.Key] = ResolvedEntitlement{Key: def.Key, Value: v, Source: "admin"}
		}
	}
	if u != nil {
		for _, src := range entitlementSources {
			values, exclusive := src.Resolve(core, u)
			for key, v := range values {
				if _, done := out[key]; done {
					continue
				}
				if _, ok := EntitlementDefByKey(key); !ok || ValidateEntitlementValue(key, v) != nil {
					continue
				}
				out[key] = ResolvedEntitlement{Key: key, Value: v, Source: src.Key}
			}
			if exclusive && values != nil {
				break
			}
		}
	}
	for _, def := range entitlementDefs {
		if _, done := out[def.Key]; done {
			continue
		}
		var base int64
		if def.Base != nil {
			base = def.Base(core)
		}
		out[def.Key] = ResolvedEntitlement{Key: def.Key, Value: base, Source: "base"}
	}
	return out
}

// EntitlementValue 用户某项权益的生效值（未登记的键返回 0）。
func EntitlementValue(core Core, u *models.User, key string) int64 {
	return ResolveEntitlements(core, u)[key].Value
}

// WithinLimit 数值权益是否允许在已有 current 个的基础上再增加一个（不限时恒为 true）。
func WithinLimit(limit, current int64) bool {
	return limit == Unlimited || current < limit
}
