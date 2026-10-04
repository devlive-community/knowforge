package plugincore

import (
	"sort"

	"knowforge/server/internal/models"
)

// —— 成员组：由某个插件管理的一组用户（如团队空间的团队），其他插件可以整体为组发放按席位计的权益（如团队会员）。——
//
// 组的提供者（如团队插件）与按组发放的插件（如会员）互不依赖，只经此扩展点协作：
//   - Members 按「席位优先次序」列出组内成员（如所有者、管理员，再按加入时间），发放方据此决定前 N 个席位覆盖谁；
//   - GroupsOf 列出用户所在的组，用于计算用户权益；
//   - Get / Managed 给出组的展示信息与当前用户能否为该组购买（如团队所有者与管理员）。

// MemberGroup 一个成员组的展示信息。
type MemberGroup struct {
	Kind        string `json:"kind"`
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Link        string `json:"link"`         // 组的站内页面
	MemberCount int    `json:"member_count"` // 当前成员数
	CanPurchase bool   `json:"can_purchase"` // 查看者能否为该组购买 / 管理按组发放的权益
}

// MemberGroupProvider 成员组提供者。
type MemberGroupProvider struct {
	Kind    string
	Enabled func(core Core) bool
	// Get 组的展示信息（viewer 用于计算 CanPurchase；为 nil 表示系统调用，只取展示信息）；
	// 组不存在或 viewer 不在组内（且不是站点管理员）时返回 false。
	Get func(core Core, viewer *models.User, id uint) (MemberGroup, bool)
	// Managed 用户能为之购买的组。
	Managed func(core Core, u *models.User) []MemberGroup
	// Members 组内成员的用户 ID（按席位优先次序）。
	Members func(core Core, id uint) []uint
	// GroupsOf 用户所在的组 ID。
	GroupsOf func(core Core, userID uint) []uint
}

var memberGroupProviders = map[string]MemberGroupProvider{}

// RegisterMemberGroupProvider 登记成员组提供者（Kind 唯一）。
func RegisterMemberGroupProvider(p MemberGroupProvider) { memberGroupProviders[p.Kind] = p }

// MemberGroupProviderFor 按类型查可用的成员组提供者（提供者所属插件未启用时视为不可用）。
func MemberGroupProviderFor(core Core, kind string) (MemberGroupProvider, bool) {
	p, ok := memberGroupProviders[kind]
	if !ok || (p.Enabled != nil && !p.Enabled(core)) {
		return MemberGroupProvider{}, false
	}
	return p, true
}

// MemberGroupProviders 当前可用的成员组提供者（按类型排序）。
func MemberGroupProviders(core Core) []MemberGroupProvider {
	kinds := make([]string, 0, len(memberGroupProviders))
	for k := range memberGroupProviders {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	out := []MemberGroupProvider{}
	for _, k := range kinds {
		if p, ok := MemberGroupProviderFor(core, k); ok {
			out = append(out, p)
		}
	}
	return out
}
