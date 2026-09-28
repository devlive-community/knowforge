package membership

import (
	"fmt"
	"strconv"
	"time"

	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 与成长、成就的联动（经 plugincore，不直接依赖其他插件）：
//   - 订单付款开通/续期后发出 membership.purchased（来源 ID 为订单号），成长插件按「购买会员」规则发经验；
//     退款撤销时发出 membership.refunded，成长插件收回该订单的经验；
//   - 会员状态每次变化发出 membership.changed，成就插件据此重新评估下列会员指标；
//   - 会员方案可配置经验加成权益（growth.xp_bonus），由成长插件在发经验时读取。

const (
	activityPurchased = "membership.purchased"
	activityRefunded  = "membership.refunded"
	activityChanged   = "membership.changed"
)

func init() {
	available := func(core plugincore.Core) bool { return core.PluginEnabled(plugins.KeyMembership) }
	plugincore.RegisterUserMetric(plugincore.UserMetric{
		Key: "membership.active", Label: "当前是会员", Description: "当前有有效的会员（试用不算）", Category: "account",
		Aggregation: "current", Windows: []string{"lifetime"}, Available: available,
		Value: func(core plugincore.Core, userID uint, _ *time.Time) (int64, error) {
			m, _ := activeMembership(core.Gorm(), userID, time.Now())
			if m == nil || m.Trial {
				return 0, nil
			}
			return 1, nil
		},
	})
	plugincore.RegisterUserMetric(plugincore.UserMetric{
		Key: "membership.total_days", Label: "累计会员天数", Description: "购买、兑换、邀请奖励与管理员开通的会员天数合计，扣除退款扣回的天数（试用不算）",
		Category: "account", Aggregation: "sum", Unit: "天", Windows: []string{"lifetime"}, Available: available,
		Value: func(core plugincore.Core, userID uint, _ *time.Time) (int64, error) {
			var granted, deducted int64
			db := core.Gorm().Model(&Record{})
			if err := db.Where("user_id = ? AND action IN ? AND source <> ?", userID, []string{ActionGrant, ActionExtend, ActionSwitch}, sourceTrial).
				Select("COALESCE(SUM(days),0)").Scan(&granted).Error; err != nil {
				return 0, err
			}
			core.Gorm().Model(&Record{}).Where("user_id = ? AND source = ?", userID, refundSource).Select("COALESCE(SUM(days),0)").Scan(&deducted)
			if granted < deducted {
				return 0, nil
			}
			return granted - deducted, nil
		},
	})
	plugincore.RegisterUserMetric(plugincore.UserMetric{
		Key: "membership.purchases", Label: "购买会员次数", Description: "付款开通或续费会员的订单数", Category: "account",
		Aggregation: "count", Unit: "次", Windows: []string{"lifetime", "rolling_days"}, Available: available,
		Value: func(core plugincore.Core, userID uint, since *time.Time) (int64, error) {
			q := core.Gorm().Model(&Record{}).Where("user_id = ? AND source = ?", userID, orderSource)
			if since != nil {
				q = q.Where("created_at >= ?", *since)
			}
			var n int64
			return n, q.Count(&n).Error
		},
	})
}

// emitChanged 会员状态变化（开通、续期、调整、取消、到期）：成就据此重新评估会员指标。
func emitChanged(core plugincore.Core, userID uint) {
	plugincore.FireActivity(core, plugincore.ActivityEvent{
		UserID: userID, Type: activityChanged, SourceType: "membership", SourceID: strconv.FormatUint(uint64(userID), 10),
		DedupeKey: fmt.Sprintf("%s:%d:%d", activityChanged, userID, time.Now().UnixNano()),
	})
}

// emitPurchased 订单付款开通/续期（每个订单一次）。
func emitPurchased(core plugincore.Core, userID uint, orderNo string) {
	plugincore.FireActivity(core, plugincore.ActivityEvent{
		UserID: userID, Type: activityPurchased, SourceType: "order", SourceID: orderNo, DedupeKey: activityPurchased + ":" + orderNo,
	})
}

// emitRefunded 订单退款并撤销（成长插件收回该订单的购买经验）。
func emitRefunded(core plugincore.Core, userID uint, orderNo, refundNo string) {
	plugincore.FireActivity(core, plugincore.ActivityEvent{
		UserID: userID, Type: activityRefunded, SourceType: "order", SourceID: orderNo, DedupeKey: activityRefunded + ":" + refundNo,
	})
}
