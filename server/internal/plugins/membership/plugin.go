// Package membership 会员插件：会员方案（权益 + 多时长定价）、用户会员（同一时间一个方案，续期顺延）、
// 管理员开通/调整/取消与到期提醒。会员有效期内作为独占的权益来源（优先于成长等级，未配置的项回退基础值）。
//
// 与支付解耦：本插件不依赖任何支付实现；在线购买由支付插件经 plugincore 的中性扩展点完成。
package membership

import (
	"knowforge/server/internal/authz"
	"knowforge/server/internal/i18ntext"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/plugins"
)

// 插件权限（启用时动态注册，禁用即移除）。
const (
	PermRead   authz.Permission = "membership:read"   // 查看自己的会员
	PermManage authz.Permission = "membership:manage" // 后台方案/会员管理（仅管理员）
)

const (
	resourceKind       = "membership_plan" // 可翻译资源：方案名称/说明
	sourceKey          = "membership"      // 权益来源键
	sourcePriority     = 100               // 高于成长等级（50）
	cfgEnabled         = "membership_enabled"
	cfgCurrency        = "membership_currency"
	cfgReminderDays    = "membership_reminder_days"
	cfgTrialVerified   = "membership_trial_verified_email"
	maxTrialDays       = 365
	defaultCurrency    = "CNY"
	defaultReminder    = 3
	maxReminderDays    = 30
	maxDurationDays    = 3650
	maxPriceCents      = 100_000_000
	notificationType   = "membership"
	notificationLink   = "/user/membership"
	expiredNoticeRange = 7 // 到期后多少天内仍补发「已到期」通知（避免启用插件时给很久以前到期的会员发通知）
)

func init() {
	plugincore.RegisterURLColumns("membership_plans", "icon_value", "description") // 存储迁移：方案图标与说明
	plugins.Register(plugins.Meta{
		Order:       90,
		Key:         plugins.KeyMembership,
		Name:        "会员",
		Description: "会员体系：多个会员方案（每个方案可配置权益与多档时长价格），用户同一时间持有一个方案、续期顺延；管理员可开通/调整/取消，到期前提醒。会员有效期内按方案权益生效（优先于成长等级）。在线购买需配合支付插件。默认关闭，禁用后页面/接口停用，数据保留。",
		Kind:        plugins.KindFeature,
		Builtin:     true,
		EnabledKey:  cfgEnabled,
		Models:      []any{&Plan{}, &Price{}, &UserMembership{}, &Record{}, &RedeemBatch{}, &RedeemCode{}, &RedeemUse{}, &GiftRefund{}, &Coupon{}, &CouponUse{}, &TrialUse{}, &ReferralReward{}, &RedeemFail{}, &Renewal{}, &GroupMembership{}, &GroupRecord{}},
		Tables:      []string{"membership_group_records", "membership_groups", "membership_renewals", "membership_redeem_fails", "membership_referral_rewards", "membership_trial_uses", "membership_coupon_uses", "membership_coupons", "membership_gift_refunds", "membership_redeem_uses", "membership_redeem_codes", "membership_redeem_batches", "membership_records", "user_memberships", "membership_prices", "membership_plans"},
		AdminPerms:  []authz.Permission{PermManage},
		UserPerms:   []authz.Permission{PermRead},
	})
	plugincore.RegisterBehavior(&behavior{})
	plugincore.RegisterUserDataModels(&UserMembership{}, &Record{}, &Renewal{})
	plugincore.RegisterLocalizedResource(resourceKind, map[string]int{"name": 120, "description": 500})
	plugincore.RegisterEntitlementSource(plugincore.EntitlementSource{Key: sourceKey, Priority: sourcePriority, Resolve: resolveEntitlements, Grants: planGrants})
	plugincore.OnJobQueueSweep(sweep)

	i18ntext.Register("notify.membership.granted", map[string]string{"zh-CN": "你已开通「{plan}」会员，有效期至 {date}", "en": "Your {plan} membership is active until {date}"})
	i18ntext.Register("notify.membership.updated", map[string]string{"zh-CN": "你的「{plan}」会员有效期已更新至 {date}", "en": "Your {plan} membership now runs until {date}"})
	i18ntext.Register("notify.membership.expiring", map[string]string{"zh-CN": "你的「{plan}」会员将于 {date} 到期", "en": "Your {plan} membership expires on {date}"})
	i18ntext.Register("notify.membership.expired", map[string]string{"zh-CN": "你的「{plan}」会员已到期", "en": "Your {plan} membership has expired"})
	i18ntext.Register("notify.membership.giftReady", map[string]string{"zh-CN": "你购买的「{plan}」礼品卡已生成，可在「我的会员」复制兑换码送给他人", "en": "Your {plan} gift card is ready. Copy its code from My membership to give it away"})
	i18ntext.Register("notify.membership.giftRedeemed", map[string]string{"zh-CN": "你送出的「{plan}」礼品卡已被兑换", "en": "Your {plan} gift card has been redeemed"})
	i18ntext.Register("notify.membership.trialStarted", map[string]string{"zh-CN": "你已开始「{plan}」免费试用，{date} 结束", "en": "Your free {plan} trial has started and ends on {date}"})
	i18ntext.Register("notify.membership.trialExpiring", map[string]string{"zh-CN": "你的「{plan}」试用将于 {date} 结束，购买后可继续使用，剩余试用天数会累加", "en": "Your {plan} trial ends on {date}. Buy now to keep it; remaining trial days carry over"})
	i18ntext.Register("notify.membership.trialExpired", map[string]string{"zh-CN": "你的「{plan}」试用已结束，购买后即可继续使用", "en": "Your {plan} trial has ended. Buy a membership to keep using it"})
	i18ntext.Register("notify.membership.referralReward", map[string]string{"zh-CN": "邀请奖励：你获得了 {days} 天「{plan}」会员，有效期至 {date}", "en": "Referral reward: {days} days of {plan}, now active until {date}"})
	i18ntext.Register("notify.membership.referralBonus", map[string]string{"zh-CN": "受邀开通会员，额外获得 {days} 天「{plan}」会员，有效期至 {date}", "en": "Thanks for joining via an invite: {days} extra days of {plan}, active until {date}"})
	i18ntext.Register("notify.membership.renewalDue", map[string]string{"zh-CN": "你的「{plan}」会员将于 {date} 到期，点击一键续费", "en": "Your {plan} membership expires on {date}. Tap to renew in one step"})
	i18ntext.Register("notify.membership.renewalFailed", map[string]string{"zh-CN": "「{plan}」会员自动续费未成功，将于 {date} 到期，请手动续费", "en": "Automatic renewal of {plan} didn't go through. It expires on {date}; please renew manually"})
	i18ntext.Register("notify.membership.renewalDisabled", map[string]string{"zh-CN": "续费计划已关闭：所选的「{plan}」时长已下架或你已更换方案，可在「我的会员」重新设置", "en": "Your renewal plan was turned off because the chosen {plan} option is no longer available or you changed plans. You can set it again under My membership"})
	i18ntext.Register("notify.membership.groupActive", map[string]string{"zh-CN": "团队「{group}」的「{plan}」团队会员（{seats} 席）有效期至 {date}", "en": "The {plan} team membership for \"{group}\" ({seats} seats) is active until {date}"})
	i18ntext.Register("notify.membership.groupCovered", map[string]string{"zh-CN": "你已通过团队「{group}」享有「{plan}」会员，有效期至 {date}", "en": "You now have {plan} through the team \"{group}\" until {date}"})
	i18ntext.Register("notify.membership.groupExpiring", map[string]string{"zh-CN": "团队「{group}」的「{plan}」团队会员将于 {date} 到期", "en": "The {plan} team membership for \"{group}\" expires on {date}"})
	i18ntext.Register("notify.membership.groupExpired", map[string]string{"zh-CN": "团队「{group}」的「{plan}」团队会员已到期", "en": "The {plan} team membership for \"{group}\" has expired"})
	i18ntext.Register("notify.membership.revoked", map[string]string{"zh-CN": "你的「{plan}」会员已被取消", "en": "Your {plan} membership has been cancelled"})
}
