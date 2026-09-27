#!/usr/bin/env python3
"""i18n 完整性检测（CI 与本地共用）。

检查项：
1. zh/en 字典键集完全一致（缺失/多余键均失败）
2. 无重复键定义（TS 对象重复键会静默覆盖）
3. 代码中引用的静态键（t('ns.x')、labelKey: 'ns.x' 等字面量）在字典中存在
4. 已登记的动态键取值域（见 DYNAMIC_KEY_SPACES）在字典中存在

用法：python3 scripts/check_i18n.py   （在 app/web 目录下执行，或传路径参数）
"""
import os
import re
import sys

WEB_DIR = sys.argv[1] if len(sys.argv) > 1 else '.'
LOCALES_DIR = os.path.join(WEB_DIR, 'lib', 'i18n', 'locales')

# 动态键取值域登记表：代码用 t(`prefix${var}suffix`) 拼接的键，静态扫描无法覆盖，
# 必须在此显式登记取值域（新增动态键拼接时请同步维护）。
DYNAMIC_KEY_SPACES: dict[str, list[str]] = {
    'i18n.': ['enabled', 'content_enabled', 'ui_enabled', 'is_default'],
    # BookCard/WriterWorkbench/detail: t(`book.status.${status}`)，BookStatus 共 5 种
    'book.status.': ['draft', 'in_progress', 'published', 'completed', 'archived'],
    # DatePicker: t(`ui.datepicker.weekday${k}`) / t(`ui.datepicker.month${m+1}`)
    'ui.datepicker.weekday': ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'],
    'ui.datepicker.month': [str(m) for m in range(1, 13)],
    # TwoFactorSettings: t(`tfa.op.${key}.label|.hint`)
    'tfa.op.': ['login', 'credentials', 'delete', 'unbind_export'],
    # admin achievements: t(`admin.achievements.category.${x}`) / rarity
    'admin.achievements.category.': ['reading', 'creation', 'community', 'account', 'special'],
    'admin.achievements.rarity.': ['common', 'rare', 'epic', 'legendary'],
    # OAuth 错误码: auth.oautherror.${code}（OAuthButtons.OAUTH_ERROR_CODES）
    'auth.oautherror.': [
        'unknown', 'not_configured', 'invalid_state', 'missing_code', 'provider_unreachable',
        'token_exchange_failed', 'profile_fetch_failed', 'account_disabled', 'register_failed',
        'token_issue_failed', 'unsupported_provider', 'registration_closed',
        'registration_invite_required', 'already_bound',
    ],
    # DocumentRevisionReason: writer.rev.${reason}
    'writer.rev.': ['create', 'save', 'publish', 'pre_restore', 'restore'],
    # crawl-history: t(`crawlHistory.status.${job.status}`)，CrawlJob.Status 取值域（含单页采集 succeeded/failed）
    'crawlHistory.status.': ['preview', 'pending', 'running', 'succeeded', 'partial', 'failed'],
    # crawl-history: t(`crawlHistory.pageStatus.${p.status}`)，CrawlPage.Status 取值域
    'crawlHistory.pageStatus.': ['pending', 'running', 'success', 'failed', 'skipped'],
    # rate-limits: t(`admin.settings.rateLimit.policy.${name}`)，allRateLimitPolicies 的 Name 取值域
    'admin.settings.rateLimit.policy.': ['auth_login', 'auth_register', 'password_forgot', 'password_reset',
                                         'comment_create', 'reaction_update', 'upload_create', 'report_create'],
    # growth: t(`growth.rule.${rule_key}`)，经验规则键取值域（服务端 growth 插件 xpCatalog + 系统规则）
    'growth.rule.': ['reading.chapter', 'creation.chapter_published', 'community.comment', 'reading.time',
                     'reading.annotation', 'creation.book_created', 'creation.chapter_created',
                     'community.comment_received', 'community.reaction', 'community.reaction_received',
                     'account.registered', 'account.email_verified', 'account.two_factor_enabled',
                     'account.oauth_bound', 'account.invited_user', 'checkin.daily', 'checkin.streak_bonus',
                     'achievement.unlocked', 'admin.adjust'],
    # admin growth: 规则说明 t(`admin.growth.ruleDesc.${rule_key}`)（xpCatalog 全部规则）与规则分组
    'admin.growth.ruleDesc.': ['reading.chapter', 'creation.chapter_published', 'community.comment', 'reading.time',
                               'reading.annotation', 'creation.book_created', 'creation.chapter_created',
                               'community.comment_received', 'community.reaction', 'community.reaction_received',
                               'account.registered', 'account.email_verified', 'account.two_factor_enabled',
                               'account.oauth_bound', 'account.invited_user', 'checkin.daily', 'checkin.streak_bonus'],
    'admin.growth.ruleGroup.': ['checkin', 'reading', 'creation', 'community', 'account', 'other'],
    # CheckinCard: t(`growth.checkin.stat.${k}`)
    'growth.checkin.stat.': ['streak', 'total', 'longest'],
    # growth: 经验流水系统原因码 t(`growth.reason.${reason}`)；排行榜周期 t(`growth.leaderboard.period.${p}`)
    'growth.reason.': ['revoked', 'achievement_revoked'],
    'growth.leaderboard.period.': ['all', 'month', 'week'],
    # 权益：t(`entitlement.${key}.label|.hint`)（服务端 plugincore.RegisterEntitlement 登记的键）、
    # t(`entitlement.unit.${unit}`) / unitShort、t(`entitlement.source.${source}`)（权益来源键）
    'entitlement.': ['books.max', 'collaborators.max', 'upload.max_mb', 'collect.page', 'collect.site', 'collect.site_max_pages',
                     'content.access_tier', 'content.discount_percent', 'content.free_all',
                     'qa.ai_daily', 'qa.agent_daily', 'ai.monthly_tokens', 'translate.monthly_chars', 'aiwriter.monthly_uses', 'translate.ai_book', 'chapterguide.monthly', 'qa.insights',
                     'export.pdf_monthly', 'export.custom_footer', 'books.private_max', 'versions.keep'],
    'entitlement.unit.': ['books', 'people', 'mb', 'pages', 'tier', 'percent', 'questions', 'tokens', 'chars', 'uses', 'exports', 'versions'],
    'entitlement.unitShort.': ['books', 'people', 'mb', 'pages', 'tier', 'percent', 'questions', 'tokens', 'chars', 'uses', 'exports', 'versions'],
    # AI 写作助手：动作 / 动作说明 / 任务状态 / 采纳方式；调用功能名 ai.feature.aiwriter.<action>
    'aiWriter.action.': ['continue', 'polish', 'rewrite', 'expand', 'shorten', 'outline', 'summary', 'custom'],
    'aiWriter.actionHint.': ['continue', 'polish', 'rewrite', 'expand', 'shorten', 'outline', 'summary', 'custom'],
    'aiWriter.status.': ['running', 'done', 'failed', 'canceled'],
    'aiWriter.adopted.': ['replace', 'insert'],
    'ai.feature.aiwriter.': ['continue', 'polish', 'rewrite', 'expand', 'shorten', 'outline', 'summary', 'custom'],
    # 整本 AI 翻译：任务方式 / 状态 / 阶段 / 章节状态
    'bookSettings.aiTranslate.mode.': ['full', 'sync'],
    'bookSettings.aiTranslate.status.': ['running', 'paused', 'done', 'failed'],
    'bookSettings.aiTranslate.stage.': ['outline', 'content', 'done'],
    'bookSettings.aiTranslate.itemStatus.': ['pending', 'running', 'done', 'failed'],
    # 章节导读：管理页状态 / 费用承担方
    'chapterGuide.state.': ['none', 'empty', 'queued', 'generating', 'ready', 'stale', 'edited', 'failed'],
    'chapterGuide.manage.bearer.': ['author', 'site'],
    'entitlement.source.': ['base', 'level', 'membership', 'admin', 'unavailable'],
    # 会员：tab / 状态 / 流水动作（Record.Action 取值域）/ 时长单位
    'admin.membership.tab.': ['plans', 'members', 'records', 'settings'],
    'admin.membership.status.': ['active', 'archived'],
    'admin.membership.action.': ['grant', 'extend', 'switch', 'adjust', 'revoke'],
    'membership.action.': ['grant', 'extend', 'switch', 'adjust', 'revoke'],
    # 书籍更多信息：t(`bookInfo.type.${type}`)（lib/book-info.ts BOOK_INFO_TYPES）、t(`bookInfo.placeholder.${kind}`)
    'bookInfo.type.': ['github', 'gitlab', 'gitee', 'website', 'source', 'docs', 'demo', 'email', 'author', 'license', 'version', 'isbn', 'custom'],
    'bookInfo.placeholder.': ['url', 'email', 'text'],
    # 内容审核：状态 / 对象类型 / 命中字段 / 管理端 tab / 空态 / 设置项（.${key} 与 .${key}Hint）
    'moderation.status.': ['auto_passed', 'pending', 'approved', 'rejected'],
    'moderation.kind.': ['document', 'book'],
    'moderation.field.': ['title', 'content', 'description'],
    'admin.moderation.tab.': ['pending', 'ai', 'auto', 'handled', 'words', 'settings'],
    'admin.moderation.empty.': ['pending', 'auto_passed', 'handled'],
    'admin.moderation.ai.status.': ['queued', 'reviewing'],
    'admin.moderation.ai.verdict.': ['safe', 'violation', 'uncertain'],
    'admin.moderation.ai.modes.': ['off', 'advise', 'auto_approve'],
    'admin.moderation.settings.': ['scope_documents', 'scope_books', 'skip_noise', 'admin_exempt', 'notify_pass',
                                   'scope_documentsHint', 'scope_booksHint', 'skip_noiseHint', 'admin_exemptHint', 'notify_passHint'],
    # 付费内容：提现状态 / 收益流水类型 / 统计项 / 管理端 tab
    'paid.withdrawal.status.': ['pending', 'paid', 'rejected'],
    'paid.earnings.kind.': ['withdrawal', 'withdrawal_revert'],
    'paid.earnings.': ['balance', 'totalNet', 'totalCommission'],
    'admin.paid.tab.': ['sales', 'withdrawals', 'settings'],
    # 支付：支付方式 / 订单状态 / 管理端 tab、订单操作（op.${op}Title|Message|Done）、支付方式说明与配置字段
    'payment.channel.': ['offline', 'alipay', 'wechat', 'stripe'],
    'payment.channelHint.': ['offline', 'alipay', 'wechat', 'stripe'],
    'payment.status.': ['pending', 'paid', 'cancelled', 'expired'],
    'admin.payment.tab.': ['orders', 'settings'],
    'admin.payment.op.': ['confirmTitle', 'confirmMessage', 'confirmDone', 'cancelTitle', 'cancelMessage', 'cancelDone', 'fulfillDone'],
    'admin.payment.settings.': ['offlineHint', 'alipayHint', 'wechatHint', 'stripeHint'],
    'admin.payment.field.': ['offline_instructions', 'offline_qr', 'offline_expire_hours', 'alipay_app_id', 'alipay_private_key', 'alipay_public_key',
                             'wechat_mch_id', 'wechat_app_id', 'wechat_serial_no', 'wechat_private_key', 'wechat_api_v3_key', 'wechat_public_key_id',
                             'wechat_public_key', 'stripe_secret_key', 'stripe_webhook_secret'],
    # 通知 payload.i18n.key（服务端 i18ntext 登记、下发，lib/notification.ts 渲染）；须与服务端模板保持同键
    'notify.': ['book.reviewed', 'comment.chapter', 'comment.reply', 'reaction.like', 'reaction.favorite', 'collab.invited', 'collab.accepted', 'collab.rejected', 'report.resolved', 'system.upgraded', 'collect.finished', 'achievement.unlocked', 'achievement.granted', 'growth.levelUp', 'follow.chapterPublished', 'membership.granted', 'membership.updated', 'membership.expiring', 'membership.expired', 'membership.revoked', 'payment.paid', 'moderation.held', 'moderation.pending', 'moderation.approved', 'moderation.rejected', 'moderation.passed', 'paid.sold', 'paid.withdrawalPaid', 'paid.withdrawalRejected'],
}

# 动态键的字段后缀（如 tfa.op.${key}.label 与 .hint 两套）
DYNAMIC_SUFFIXES: dict[str, list[str]] = {
    'tfa.op.': ['.label', '.hint'],
    'entitlement.': ['.label', '.hint'],
}

SKIP_DIRS = {'node_modules', '.next', '.next-build', 'lib/i18n/locales', 'coverage', 'scripts'}

# 已确认不是 i18n 键的字符串字面量（与键前缀同名碰撞的数据值）
# 新增碰撞时在此登记，并注明出处
KNOWN_NON_KEYS = {
    # 权益键（lib/entitlements.ts entitlementAllowed 的参数，数据值，文案键为 entitlement.<key>.label）
    'collect.page', 'collect.site', 'export.custom_footer',
    # components/MyEntitlementsCard.tsx 按月计量权益的键（数据值）
    'ai.monthly_tokens', 'translate.monthly_chars',
    # pages/admin/audit-logs.tsx 审计 action 下拉的 value（数据值，文案键为 admin.audit.action.*）
    'user.role_updated', 'user.status_updated', 'user.deleted',
    'book.moderated', 'book.permanently_deleted', 'report.resolved',
    # pages/admin/growth: 经验流水规则筛选的系统规则键（数据值，文案键为 growth.rule.*）
    'admin.adjust',
    # pages/admin/audit-logs: 审计操作标识（数据值，文案键为 admin.audit.actions.*）
    'i18n.messages_updated',
}

KEY_RE = re.compile(r"'([a-zA-Z][\w.]*)'\s*:")
USED_RE = re.compile(r"'([a-zA-Z][\w.]*)'")


def load_dict(name: str) -> tuple[set[str], list[str]]:
    """返回 (键集合, 重复键列表)"""
    path = os.path.join(LOCALES_DIR, name)
    keys: list[str] = []
    for i, line in enumerate(open(path, encoding='utf-8'), 1):
        m = KEY_RE.search(line)
        if m:
            keys.append(m.group(1))
    seen: set[str] = set()
    dups: list[str] = []
    for k in keys:
        if k in seen:
            dups.append(k)
        seen.add(k)
    return seen, dups


def main() -> int:
    errors: list[str] = []

    # 1/2. 加载字典并比对
    zh, zh_dups = load_dict('zh.ts')
    en, en_dups = load_dict('en.ts')
    for loc, dups in (('zh', zh_dups), ('en', en_dups)):
        for d in dups:
            errors.append(f'[{loc}] 重复键定义: {d}')
    for k in sorted(zh - en):
        errors.append(f'[en] 缺失键: {k}')
    for k in sorted(en - zh):
        errors.append(f'[zh] 缺失键: {k}')

    # 命名空间前缀集合（用于从代码里识别 i18n 键字面量）
    prefixes = sorted({k.split('.')[0] for k in zh | en}, key=len, reverse=True)
    prefix_alt = '|'.join(re.escape(p) for p in prefixes)
    used_re = re.compile(rf"'(({prefix_alt})\.[\w.]+)'")

    # 3. 扫描代码中的键字面量
    used: dict[str, str] = {}
    for root, dirs, files in os.walk(WEB_DIR):
        rel = os.path.relpath(root, WEB_DIR).replace('\\', '/')
        if any(rel == s or rel.startswith(s + '/') for s in SKIP_DIRS):
            continue
        for f in files:
            if not f.endswith(('.ts', '.tsx')):
                continue
            p = os.path.join(root, f)
            for i, line in enumerate(open(p, encoding='utf-8'), 1):
                for m in used_re.finditer(line):
                    used.setdefault(m.group(1), f'{os.path.relpath(p, WEB_DIR)}:{i}')

    for k in sorted(used):
        if k not in zh and k not in KNOWN_NON_KEYS:
            errors.append(f'字典缺失被引用的键: {k}  <- {used[k]}')

    # 4. 动态键取值域展开检查
    for prefix, values in DYNAMIC_KEY_SPACES.items():
        suffixes = DYNAMIC_SUFFIXES.get(prefix, [''])
        for v in values:
            for sfx in suffixes:
                key = f'{prefix}{v}{sfx}'
                if key not in zh:
                    errors.append(f'动态键缺失: {key}（取值域登记表 DYNAMIC_KEY_SPACES）')
                if key not in en:
                    errors.append(f'动态键缺失(en): {key}')

    if errors:
        print(f'❌ i18n 检查失败，共 {len(errors)} 个问题：')
        for e in errors:
            print(f'  - {e}')
        return 1
    print(f'✅ i18n 检查通过：zh/en 各 {len(zh)} 键，同步一致；引用 {len(used)} 键全部存在；动态键取值域 {sum(len(v) for v in DYNAMIC_KEY_SPACES.values())} 项全覆盖')
    return 0


if __name__ == '__main__':
    sys.exit(main())
