package plugincore

import "time"

// —— 用户指标：插件为成就等提供「用户在某时间窗内的某项数值」，供成就规则引用（如会员累计天数）。——
// 成就插件把登记的指标与内置指标合并展示和评估；指标所属插件未启用时不出现在规则构建器中，评估记 0。
// 指标变化时，提供方应发出业务活动（FireActivity），成就插件据此重新评估该用户。

// UserMetric 一项用户指标。Category / Aggregation / Windows 的取值与成就内置指标一致
// （如 account；count / sum / current；lifetime / rolling_days / calendar_month …）。
type UserMetric struct {
	Key         string
	Label       string // 默认文案（前端优先使用 admin.achievements.metric.<key>.label）
	Description string
	Category    string
	Aggregation string
	Unit        string
	Windows     []string
	// Available 指标当前是否可用（如所属插件已启用）；nil 表示始终可用。
	Available func(core Core) bool
	// Value 用户在窗口内的取值；since 为 nil 表示全部时间。
	Value func(core Core, userID uint, since *time.Time) (int64, error)
}

var userMetrics []UserMetric

// RegisterUserMetric 供插件在 init() 中登记用户指标。
func RegisterUserMetric(m UserMetric) { userMetrics = append(userMetrics, m) }

// UserMetrics 返回全部已登记的用户指标（登记顺序）。
func UserMetrics() []UserMetric { return userMetrics }

// UserMetricByKey 按键查找已登记的用户指标。
func UserMetricByKey(key string) (UserMetric, bool) {
	for _, m := range userMetrics {
		if m.Key == key {
			return m, true
		}
	}
	return UserMetric{}, false
}
