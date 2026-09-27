# KnowForge API 接口契约

> 本文档是全部 REST API 的权威契约，供 Web / 桌面端（Tauri）/ Android 客户端开发使用。
> 任何端点变更必须同步更新本文档。

- Base URL：`{服务器地址}/api/v1`，生产环境同源部署（nginx 分流），无需跨域配置
- 响应格式：统一 JSON 信封 `{ "success": true, "data": ... }` 或 `{ "success": false, "message": "...", "code": "..." }`
- 时间格式：RFC3339（如 `2026-09-05T12:00:00+08:00`）
- 分页参数：`page`（默认 1）、`page_size`（默认 12，最大 100）；分页响应 `{ items, total, page, page_size }`

## 动态国际化

语言使用规范化 BCP 47 代码（旧 `zh` 偏好兼容为 `zh-CN`）。界面语言包和内容翻译分别启用。语言选择依次使用 `locale` 查询参数、`X-KnowForge-Locale`、当前用户偏好、`knowforge_locale` Cookie、`Accept-Language`、站点默认语言。业务逻辑标识、价格和权限不随语言变化。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/i18n/locales` | `{items,default_locale,locale}`；只返回启用语言，每条含 `code/native_name/direction/ui_enabled/content_enabled/is_default/fallback_locale/sort_order` | 匿名，可选登录 |
| GET | `/i18n/messages/:locale` | `{locale,chain,messages}`；按语言提供已发布覆盖消息，客户端与内置语言包按 chain 合并；ETag/304 缓存 | 匿名 |
| PUT | `/auth/locale` | `{locale}` 保存本人语言偏好，同时设置语言 Cookie | `user:update` |
| GET/PUT | `/admin/i18n/locales` | GET 返回 `{items,revision}`；PUT 提交完整 items 和读取时 revision；禁止删除已有语言，可停用；校验唯一默认语言、有效回退与循环 | `i18n:manage`，仅管理员 |
| GET | `/admin/i18n/messages/:locale` | `{locale,revision,draft,published}`，包括停用语言的历史语言包 | `i18n:manage`，仅管理员 |
| PUT | `/admin/i18n/messages/:locale` | `{messages,revision,publish}`；覆盖该语言草稿，publish=true 同时更新发布版本。最大 2MB / 10000 条；后台编辑器检查 ICU 语法和变量 | `i18n:manage`，仅管理员 |
| GET/PUT | `/admin/i18n/resources/achievement/:id` | 读取/部分更新成就的动态内容翻译，PUT `{translations}` 只写传入的语言（由「成就系统」插件注册；可翻译资源类型由插件经 `plugincore.RegisterLocalizedResource` 登记） | `achievement:manage`，仅管理员 |

成就 POST/PUT 也接受 `translations`，与定义、规则和版本快照同事务保存。例如：

```json
{"translations":{"ja":{"fields":{"name":"読書家","description":"読書を続ける","locked_hint":""},"revision":0,"publish":true}}}
```

每种语言独立乐观锁，旧 revision 返回 409；未提交语言保持不变。字段允许 `name/description/locked_hint`，长度上限分别为 120/500/255 字符。保存草稿不改变已发布内容；激活成就要求默认语言名称已发布。显式空说明表示清空，字段缺省按回退链解析。停用语言保留数据但不参与内容回退。

成就管理响应携带完整 translations；公开成就与我的成就只返回解析后的 `name/description/locked_hint` 和 `resolved_locale`，不返回翻译草稿。旧 name/name_en 等列保留作为兼容字段，升级自动回填动态翻译，重复启动不会覆盖人工翻译。隐藏成就仍执行原隐私规则。

系统界面继续兼容现有 `t(key, vars)`，支持 ICU 复数、选择与数字格式；缺失覆盖消息回退到已发布的父语言/指定回退语言/站点默认和内置字典。语言包缺失不妨碍录入对应语言内容。新增语言不表示自动生成所有翻译。

Web 为 Cookie/用户偏好提供一致首屏，在 `_app.getInitialProps` 注入语言快照；原自动静态优化页面改为按请求渲染（现有 URL 不变），HTML 使用 `private,no-store` 避免跨用户语言缓存。公开多语言 SEO 路由、旧邮件模板改造不属于当前接口。

## 登录令牌

除标注「匿名」的端点外，请求需携带 JWT：

```
Authorization: Bearer <token>
```

- 令牌在登录 / 注册 / 安装完成时签发，有效期 7 天，HS256 签名
- 签发时**同时下发 `knowforge_token` Cookie**（7 天，非 HttpOnly）：Web SSR 凭 Cookie 在服务端渲染登录态；客户端仍用 `Authorization: Bearer` 或同源 Cookie 均可
- 登出时客户端清除 localStorage 并使 Cookie 过期（`Max-Age=0`）
- `GET /auth/permissions` 可获取当前用户权限列表，客户端据此控制 UI 可见性

## 权限模型

权限标识格式为 **`功能:权限`**（`resource:action`），定义在 `server/internal/authz/authz.go`。

| 权限 | 说明 | user 角色 | admin 角色 |
| --- | --- | :-: | :-: |
| `book:read` | 浏览书籍列表与详情 | ✅ | ✅ |
| `book:create` | 创建书籍 | ✅ | ✅ |
| `book:update` | 更新书籍（仅本人） | ✅ | ✅ |
| `book:delete` | 将书籍移入回收站（仅本人） | ✅ | ✅ |
| `book-analytics:read` | 查看本人书籍的聚合访问分析（管理员可查看全部） | ✅ | ✅ |
| `document:read` | 浏览文档树与正文 | ✅ | ✅ |
| `document:create` | 创建文档（仅本人书籍） | ✅ | ✅ |
| `document:update` | 更新文档（仅本人书籍） | ✅ | ✅ |
| `document:delete` | 将文档子树移入回收站（owner/admin/editor） | ✅ | ✅ |
| `document-revision:read` | 查看章节版本历史（所有者/admin/editor） | ✅ | ✅ |
| `document-revision:restore` | 恢复章节历史版本（所有者/admin/editor） | ✅ | ✅ |
| `trash:read` | 查看回收站（普通用户仅自己的内容，管理员可查看全站） | ✅ | ✅ |
| `trash:restore` | 恢复回收站内容（书籍 owner/admin；章节 owner/admin/editor） | ✅ | ✅ |
| `trash:delete` | 永久删除回收站内容（仅资源 owner/admin） | ✅ | ✅ |
| `user:read` | 查看用户公开主页 | ✅ | ✅ |
| `user:update` | 更新个人资料与密码 | ✅ | ✅ |
| `user:manage` | 管理后台管理用户：列表/角色/启停/删除 | ❌ | ✅ |
| `site:read` | 读取站点公开配置 | ✅ | ✅ |
| `tag:read` | 浏览标签与按标签检索 | ✅ | ✅ |
| `tag:create` | 创建标签（书籍打标时自动创建） | ✅ | ✅ |
| `tag:delete` | 删除标签 | ❌ | ✅ |
| `tag:manage` | 后台标签管理（重命名、图标、列出全部） | ❌ | ✅ |
| `search:read` | 全局搜索书籍与章节（匿名仅公开内容） | ✅ | ✅ |
| `comment:read` | 浏览章节评论（匿名可读） | ✅ | ✅ |
| `comment:create` | 发表评论 | ✅ | ✅ |
| `comment:update` | 编辑自己的评论 | ✅ | ✅ |
| `comment:delete` | 删除评论（本人/书籍作者/管理员） | ✅ | ✅ |
| `reaction:create` | 点赞/收藏书籍 | ✅ | ✅ |
| `reaction:delete` | 取消点赞/收藏 | ✅ | ✅ |
| `reaction:read` | 查看自己的点赞/收藏 | ✅ | ✅ |
| `reading-progress:read` | 查看自己的阅读进度 | ✅ | ✅ |
| `reading-progress:update` | 保存自己的阅读进度 | ✅ | ✅ |
| `annotation:read` | 查看自己的阅读标注、笔记和书签 | ✅ | ✅ |
| `annotation:create` | 创建自己的阅读标注、笔记和书签 | ✅ | ✅ |
| `annotation:update` | 更新自己的阅读标注和锚点状态 | ✅ | ✅ |
| `annotation:delete` | 删除自己的阅读标注、笔记和书签 | ✅ | ✅ |
| `report:create` | 举报当前有权查看的书籍、章节或评论 | ✅ | ✅ |
| `report:read` | 查看举报队列、举报人身份和处理记录 | ❌ | ✅ |
| `report:update` | 驳回举报或下架被举报内容 | ❌ | ✅ |
| `auth:oauth` | 管理第三方登录绑定 | ✅ | ✅ |
| `auth:password-reset` | 申请/执行密码重置（匿名语义，端点公开） | ✅ | ✅ |
| `notification:read` | 查看自己的通知（含 SSE 流） | ✅ | ✅ |
| `notification:update` | 标记通知已读 | ✅ | ✅ |
| `collaborator:read` | 查看书籍协作者列表 | ✅ | ✅ |
| `collaborator:create` | 添加/更新协作者（仅书籍所有者/管理员） | ✅ | ✅ |
| `collaborator:update` | 接受或拒绝发给自己的协作邀请 | ✅ | ✅ |
| `collaborator:delete` | 移除协作者（所有者；协作者可自行退出） | ✅ | ✅ |
| `book:export` | 导出书籍为 markdown zip（owner/admin/editor 协作者） | ✅ | ✅ |
| `book:import` | 从 zip、PDF 或网页导入书籍（成为导入者的私有草稿） | ✅ | ✅ |
| `site:update` | 更新站点配置 | ❌ | ✅ |
| `config:manage` | 管理任意系统配置键值对 | ❌ | ✅ |
| `audit:read` | 查看管理员高风险操作审计日志 | ❌ | ✅ |
| `task:read` | 查看持久化异步任务状态与失败诊断 | ❌ | ✅ |
| `task:retry` | 将最终失败的异步任务重新排队 | ❌ | ✅ |
| `stats:read` | 读取站点统计 | ✅ | ✅ |
| `upload:create` | 上传图片 | ✅ | ✅ |
| `system:read` | 查看系统版本信息 | ❌ | ✅ |
| `system:upgrade` | 触发在线升级 | ❌ | ✅ |
| `plugin:manage` | 管理后台插件安装/卸载 | ❌ | ✅ |
| `achievement:read` | 查看自己的成就与进度 | ✅ | ✅ |
| `achievement:update` | 修改自己的成就公开与置顶设置 | ✅ | ✅ |
| `achievement:manage` | 管理成就模块、定义、规则与图标 | ❌ | ✅ |
| `achievement:grant` | 人工授予或撤销用户成就 | ❌ | ✅ |

补充规则：

- 标注「匿名」的端点无需令牌即可访问（公开内容）；携带令牌可看到自己可见的私有内容
- 归属校验在服务端 handler 内完成：拥有 `book:update` 只能改自己的书，admin 可改所有
- 权限不足返回 `403 { code: "PERMISSION_DENIED", message: "权限不足，需要 xxx" }`

## 全局错误

| HTTP | 场景 |
| --- | --- |
| 400 | 参数错误 |
| 401 | 未登录 / 令牌无效 |
| 403 | 权限不足 / 无权访问该资源 |
| 404 | 资源不存在 |
| 429 | `{ code: "RATE_LIMITED", retry_after: 秒数 }` 请求过于频繁；同时返回 `Retry-After`、`X-RateLimit-Limit` 与 `X-RateLimit-Remaining` 响应头 |
| 503 | `{ code: "NOT_INSTALLED" }` 系统尚未安装（仅安装向导与健康检查可用） |

---

## 请求限流

限流状态当前保存在单个服务进程内；键只由策略、用户 ID 或客户端 IP 的摘要组成，不读取或保存密码、令牌、邮箱与请求正文。登录后的写操作优先按用户 ID 隔离，匿名认证操作按客户端 IP 隔离。

| 操作 | 限额 |
| --- | --- |
| 登录 | 每 IP 10 次 / 5 分钟 |
| 注册 | 每 IP 5 次 / 小时 |
| 申请找回密码 | 每 IP 5 次 / 小时 |
| 执行密码重置 | 每 IP 10 次 / 小时 |
| 发布评论 | 每用户 30 次 / 分钟 |
| 点赞、收藏及取消 | 每用户 120 次 / 分钟 |
| 上传图片 | 每用户 20 次 / 分钟 |
| 提交内容举报 | 每用户 20 次 / 小时 |

默认不信任 `X-Forwarded-For` 等代理头。只有反向代理的地址或 CIDR 被显式配置到 `KNOWFORGE_TRUSTED_PROXIES` 后，服务才使用其传入的客户端 IP；多个值使用英文逗号分隔。

---

## 基础

| 方法 | 路径 | 说明 | 认证 |
| --- | --- | --- | --- |
| GET | `/health` | 健康检查（nginx/CI 用），返回 db/版本/commit | 匿名 |

`GET /api/v1/health` →
```json
{ "status": "ok", "db": "up", "installed": true, "version": "2026.0.0", "commit": "6eceb7d", "build_date": "..." }
```

## 安装向导（仅未安装时可用）

| 方法 | 路径 | 说明 | 认证 |
| --- | --- | --- | --- |
| GET | `/setup/status` | 安装状态、版本、可选数据库类型；仅未安装时返回默认数据目录与 SQLite 路径 | 匿名 |
| POST | `/setup/test-connection` | 测试数据库连接；安装完成后返回 404 | 匿名，仅未安装 |
| POST | `/setup/install` | 执行安装（迁移建表 + 站点配置 + 管理员），成功即登录 | 匿名 |

`POST /setup/install` 请求体：
```json
{
  "database": { "type": "sqlite", "path": "" },
  "site": { "name": "我的知识库", "description": "一句话介绍" },
  "admin": { "username": "admin", "email": "a@b.c", "password": "至少6位" }
}
```
`database.type` ∈ `sqlite | mysql | postgres`；sqlite 留空 `path` 使用默认；mysql/postgres 需 `host/port/name/user/password`。

## 认证与会话

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/auth/registration` | 注册页读取注册方式：`{mode: open\|open_invite\|invite\|closed, require_email}` | 匿名 |
| POST | `/auth/register` | 注册，返回 token + user；受注册方式门禁：`closed` 拒绝、`invite`/`open_invite` 需 `invite_code`（他人专属邀请码）、`require_email` 时邮箱必填；开启激活时新用户 `email_verified=false` 并发激活邮件 | 匿名 |
| POST | `/auth/login` | 登录（用户名或邮箱），返回 token + user | 匿名 |
| POST | `/auth/email/verify` | 匿名凭令牌激活邮箱：`{token}`（一次性、24h 有效） | 匿名 |
| POST | `/auth/email/resend` | 登录用户重发激活邮件 | 登录 |
| GET/POST/DELETE | `/auth/invite-code` | 邀请码 opt-in：GET 返回 `{invite_code, enabled}`；POST 开启（首次可选自定义 `{code}`，4-20 位字母数字、全站唯一、只能设置一次，不传则自动生成）；DELETE 停用（保留邀请码，再开启仍是同一个）。仅启用中的邀请码可用于注册 | 登录 |
| GET | `/auth/invited` | 我邀请的用户列表 `{items:[{username,avatar,created_at}],total}`（关闭邀请码后仍可查看） | 登录 |
| GET/PUT | `/auth/notification-prefs` | 邮件通知偏好：GET 返回 `{email_enabled(站点总开关), prefs:{comment,reaction,collaboration,moderation,system,achievement}}`；PUT 保存 `prefs`（缺省全开） | 登录 |
| GET | `/auth/2fa` | 二次认证状态 `{enabled, operations:[login\|credentials\|delete\|unbind_export]}` | 登录 |
| POST | `/auth/2fa/setup` | 预配置 TOTP：返回 `{secret, otpauth_url, qr(data-uri)}`（尚未开启） | 登录 |
| POST | `/auth/2fa/enable` | 校验 `{code}` 后开启，默认勾选全部敏感操作，返回一次性 `backup_codes` | 登录 |
| POST | `/auth/2fa/disable` | 校验 `{code}`（动态码/备用码）后关闭并清除密钥与备用码 | 登录 |
| PUT | `/auth/2fa/operations` | 保存需二次认证的操作集合 `{operations:[]}` | 登录 |
| POST | `/auth/2fa/verify` | step-up 验证 `{code}`，成功授予 5 分钟窗口（受保护操作据此放行；否则返回 403 `TWO_FACTOR_REQUIRED`） | 登录 |
| POST | `/auth/2fa/backup-codes` | 校验 `{code}` 后重置并返回新 `backup_codes` | 登录 |
| GET/PUT | `/registration` | 管理员读取/保存注册设置：`{mode, require_email, require_activation}` | `site:update` |
| GET | `/captcha?scene=register\|login\|comment` | 场景验证码：未开启返回 `{required:false}`；开启返回 `{required:true, id, type, image(data-uri)/question}`；提交对应操作时带 `captcha_id`+`captcha_answer` | 匿名 |
| GET/PUT | `/captcha-settings` | 管理员读取/保存验证码设置：`{type(image\|arithmetic), length, charset(digit\|alnum), noise(0-3), arith_hard, on_register, on_login, on_comment}` | `site:update` |
| GET/PUT | `/login-security` | 管理员读取/保存登录安全：`{lockout_enabled, lockout_threshold, lockout_window(分钟), lockout_duration(分钟), password_min_length(≥6), password_require_mixed, account_deletion_cooldown_days(0-90)}`。登录连续失败达阈值临时锁定账户（429）；密码策略作用于注册/改密/找回；`account_deletion_cooldown_days` 为自助注销冷静期（0 表示确认后立即删除） | `site:update` |
| GET/PUT | `/content-settings` | 管理员读取/保存内容设置：`{upload_max_mb(1-100), upload_allowed_exts(逗号分隔扩展名), comments_enabled}`。上传超限/类型不符拒绝；`comments_enabled=false` 时全站禁止发表评论（`comments_enabled` 也会出现在公开 `/site`，供前端隐藏评论框） | `site:update` |
| GET | `/auth/me` | 当前用户信息（含 `email_verified`、`invite_code`，以及 `entitlements{key:value}` 各项权益的生效值，见「权益」） | 登录 |
| GET | `/auth/permissions` | 当前用户权限列表（`string[]`） | 登录 |
| PUT | `/auth/profile` | 更新资料（email/avatar/bio/github_url/nickname/website/location/company；改邮箱受二次认证保护） | `user:update` |
| GET/PUT | `/auth/export-settings` | 当前用户 PDF 导出样式偏好：`page_size`(A4\|Letter)、`include_cover`、`include_toc`、`font_size`(12–20)、`code_theme`(light\|dark)、`margin`(narrow\|normal\|wide)、`footer`（每页页脚 Powered by 文案，≤100 字，留空用默认 `Powered by <站点名>`） | `user:read` / `user:update` |
| PUT | `/auth/password` | 修改密码（old_password/new_password；OAuth 用户未设密码时免验原密码，用于首次设置） | `user:update` |
| POST | `/auth/password/forgot` | 匿名申请找回：`{email}`；响应不泄露邮箱是否存在，令牌邮件 60 分钟有效、一次性、只保留最新一条；邮件写入持久化异步队列，失败自动退避重试；`mail_driver=log` 时执行任务后把链接输出到后端日志 | `auth:password-reset`（匿名语义） |
| POST | `/auth/password/reset` | 匿名重置：`{token, password}`（≥6 位）；成功后旧密码立即失效，该用户其余令牌作废 | `auth:password-reset`（匿名语义） |
| GET | `/auth/account/deletion` | 当前用户注销状态：`{requested, cooldown_days, deletion_requested_at?, scheduled_delete_at?}` | `user:read` |
| POST | `/auth/account/deletion` | 申请注销账号：`{password}`（设有密码时校验）+ 二次认证；冷静期>0 进入冷静期并返回状态，冷静期=0 立即彻底删除账号及全部数据（书籍/章节/版本/互动/进度/评论/绑定/设置）返回 `{deleted:true}`；最后一位启用中的管理员不可注销 | `user:update` |
| DELETE | `/auth/account/deletion` | 冷静期内撤销注销申请，返回最新状态 | `user:update` |

## 第三方登录（OAuth，支持 github / google / gitlab）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/auth/oauth/providers` | 各 provider 启用状态：`{providers:[{provider,enabled}]}` | 匿名 |
| GET | `/auth/oauth/:provider` | 发起登录：302 到授权页；回跳地址固定使用管理员配置的 `site_url`，未配置时使用当前服务地址 | 匿名 |
| GET | `/auth/oauth/:provider/callback` | 授权回调：换取用户 → 已绑定直接登录 / 已验证邮箱自动关联 / 自动注册；签发 token + Cookie 后回到可信站点地址 | 匿名 |
| POST | `/auth/oauth/:provider/link` | 已登录用户绑定第三方账号：用带当前用户 id 的 state 走授权流程，回调按当前账号绑定（不依赖邮箱匹配），返回 `{redirect}` 供前端跳转；该第三方账号已被他人绑定时回跳 `?oauth_error=already_bound` | `auth:oauth` |
| GET | `/auth/oauth/bindings` | 当前用户绑定列表 `[{provider,provider_username,provider_email,created_at}]` | `auth:oauth` |
| DELETE | `/auth/oauth/:provider` | 解绑；未设置本地密码时拒绝（防止锁死） | `auth:oauth` |
| GET/PUT | `/oauth` | 管理员读取/保存各 provider 凭据：GET 返回 `{providers:[{provider,label,client_id,client_secret,enabled}]}`；PUT 保存单个 `{provider,client_id,client_secret,enabled}`（存 `oauth_<provider>_*` 键，不出现在公开 `/site`） | `site:update` |
| GET/PUT | `/mail` | 管理员读取/保存邮件配置（driver log\|smtp、host/port/username/password/from）、`site_url`（邮件链接前缀）与 `notifications_enabled`（站内通知是否同时发邮件的总开关） | `site:update` |
| GET/PUT | `/storage` | 管理员读取/保存存储驱动配置：`driver` local\|qiniu\|s3 + 七牛凭据（access_key/secret_key/bucket/domain/upload_host，域名须含协议）+ S3 兼容存储（`s3_endpoint`、`s3_region`、`s3_bucket`、`s3_access_key`、`s3_secret_key`（只写：GET 仅返回 `s3_secret_key_set`，PUT 传空串不修改）、`s3_public_url`、`s3_path_style`、`s3_prefix`；适用 AWS S3 / 阿里云 OSS / 腾讯云 COS / MinIO / R2，Signature V4） | `site:update` |

> 凭据存于站点配置（`oauth_github_*` 键）；state 防 CSRF 为内存态（10 分钟 TTL），适配当前单实例部署架构。

## 站点与统计（公开）

| 方法 | 路径 | 说明 | 语义权限 |
| --- | --- | --- | --- |
| GET | `/site` | 站点公开配置（site_name/site_description/site_logo/site_favicon/site_keywords/site_footer_text/site_beian/help_doc_url/terms_url/privacy_url/version/comments_enabled/announcement_*，以及字符串形式的 `achievements_enabled`） | `site:read` |
| PUT | `/site` | 更新站点配置：`site_name`、`site_description`、`site_logo`、`site_favicon`（浏览器标签图标 URL）、`site_keywords`（SEO 关键词）、`site_footer_text`（页脚介绍）、`site_beian`（ICP 备案号）、`help_doc_url`（写作台帮助文档链接）、`terms_url`（用户协议链接）、`privacy_url`（隐私政策链接，三者通常指向某本书的某个章节 reader 链接），以及全站公告 `announcement_enabled`/`announcement_text`/`announcement_tone`(info\|warning)。图片经 `/upload` 上传，遵循当前存储驱动（local\|qiniu） | `site:update` |
| GET | `/stats` | 公开站点统计；书籍、章节、标签和浏览量仅统计公开且处于可阅读状态（进行中/已发布/已完成）的内容 | `stats:read` |

## 发现（公开）

| 方法 | 路径 | 说明 | 语义权限 |
| --- | --- | --- | --- |
| GET | `/explore/hot` | 浏览量最高的 6 本公开书籍 | `book:read` |
| GET | `/explore/latest` | 最新发布的 6 本公开书籍 | `book:read` |
| GET | `/search?q=&type=&book=&author=&tag=&updated_from=&updated_to=&page=&page_size=` | 高级全文搜索；`type` 为 all/book/document，作者使用用户名、标签使用 slug、日期为 YYYY-MM-DD。可选 `book=<slug>` 限定在某本书内搜索章节（此时强制 `type=document`，不返回书籍结果）。返回书籍/章节分页结果及 `book_total/document_total/total/page/page_size`。匿名仅查公开可读书籍及已发布章节；owner/admin/editor 可搜索草稿，viewer 仅可搜索已发布章节 | `search:read` |

搜索优先使用当前数据库原生全文索引（SQLite FTS5、MySQL FULLTEXT、PostgreSQL tsvector）；数据库能力或建索引权限不足时自动回退 LIKE。全文索引仅负责命中候选，权限过滤始终在查询中独立执行。`type=all` 将两类结果按更新时间合并后分页；`total` 表示当前类型的总数，两个分类总数始终分别返回。

## 用户（公开主页）

| 方法 | 路径 | 说明 | 语义权限 |
| --- | --- | --- | --- |
| GET | `/users/:username` | 用户公开资料（含 nickname/website/location/company/github_url/bio）与公开书籍数 | `user:read` |
| GET | `/users/:username/books?page=&group_versions=` | 该用户的公开书籍（分页）；`group_versions=true` 时按版本组聚合（同 `/books`） | `user:read` |
| GET | `/users/:username/achievements` | 该用户允许公开的已解锁成就；模块或公开陈列关闭时返回 `{enabled:false,items:[]}` | 匿名，语义 `achievement:read` |

## 成就模块

成就模块默认关闭。定义支持 `draft|active|paused|archived` 状态、阅读/创作/社区/账号/特殊分类、普通/稀有/史诗/传奇稀有度、FA/图片/净化 SVG 图标、系列等级，以及最多 10 条 `all|any` 白名单指标规则。规则只接受服务端指标目录，不接受 SQL 或脚本。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/achievements/settings` | 公开模块状态 `{enabled,public_profile_enabled,showcase_limit}` | 匿名 |
| GET | `/users/me/achievements` | 返回本人全部 active 成就、后台计算的规则进度与授予记录；隐藏成就解锁前不返回真实规则、进度和图标 | `achievement:read` |
| PUT | `/users/me/achievements/:id/display` | 修改本人已解锁成就 `{is_public?,showcase_order?}` | `achievement:update` |

时间窗口支持 `lifetime|calendar_day|calendar_week|calendar_month|rolling_days`（具体以 `GET /admin/achievement-metrics` 返回的每指标 `windows` 为准）；比较方式支持 `gte|eq|between`。

## 权益（等级特权 / 会员）

按用户生效的能力上限与开关。核心登记 `books.max`（书籍数量，含导入/复制/采集新建，不含回收站）、`ai.monthly_tokens`（每月 AI 用量 tokens，所有 AI 功能合计，默认不限，配置了 AI 服务才可用）、`translate.monthly_chars`（每月翻译字数，默认不限，配置了翻译服务才可用）、`collaborators.max`（单本书协作者人数，含待接受邀请，按书籍所有者计）、`upload.max_mb`（单文件上传大小）；内容采集插件登记 `collect.page`、`collect.site`（开关）与 `collect.site_max_pages`。数值 `-1` 表示不限，开关 1 开 / 0 关。

- **基础值**：全站默认，未配置时与升级前一致（书籍/协作者不限，上传与采集沿用原设置），管理员主动收紧才生效。
- **来源**：插件登记（成长等级：当前等级及以下启用等级的累计配置；会员：有效期内独占，未配置的键回退基础值，见「会员」），高优先级来源先取值。
- **管理员**不受限制；所属插件禁用时对应权益恒为不可用（`source=unavailable`）。超限时接口返回 403。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/entitlements/definitions` | 权益定义 `[{key,kind:limit\|flag,unit,min,max,allow_unlimited}]`（供等级/会员权益编辑器） | 登录 |
| GET | `/users/me/entitlements` | 我的各项权益 `items:[{key,value,source}]`（`source` 为 base/admin/unavailable 或来源键如 level）+ `definitions` | 登录 |
| GET | `/users/me/ai-usage` | 本月 AI 用量：`used_tokens`、`calls`、`limit`（权益 `ai.monthly_tokens`，-1 不限；超出后本月内 AI 调用返回「本月 AI 用量已达上限」）、按功能分布 `by_feature[]`、本月每日用量 `daily[]{date, tokens, characters}`，以及本月翻译字数 `translate_chars` 与额度 `translate_limit` | 登录 |
| GET | `/users/me/ai-usage/logs?page=&page_size=&feature=&trace_id=` | 我的全部模型调用，按调用链（`trace_id`，同一次操作如一次提问的多次调用）分组，新→旧：`items[]{trace_id, feature, ref_type, ref_id, started_at, ended_at, calls, errors, input_tokens, output_tokens, characters, duration_ms, items[]}`，每次调用含 `kind`（chat\|embed\|translate）、`model`、tokens/字数、`estimated`、`duration_ms`、`status`；不返回费用与 AI 服务的原始报错 | 登录 |
| GET | `/users/me/ai-usage/export?feature=` | 以 CSV（UTF-8 BOM，便于 Excel 打开）导出我的全部调用记录：`time, trace_id, feature, kind, model, input_tokens, output_tokens, characters, estimated, duration_ms, status`，不含费用 | 登录 |
| GET | `/admin/entitlements` | 权益定义 + 基础值 `items:[{...定义,base,available}]` | `site:update` |
| PUT | `/admin/entitlements/base` | `{values:{key:value}}` 只保存传入的键；`upload.max_mb` 基础值最大 100（更大通过等级/会员授予）；写审计 `entitlement.base_updated` | `site:update` |

## 书籍

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books?page&page_size&title&status&mine&group_versions` | 列表；默认公开书籍，`mine=true` 查自己的（需登录）。「书籍版本」插件启用时：`group_versions=true` 在服务端按 `version_group` 聚合（每组只保留标记为最新版的一本，否则最新创建的一本，分页总数按聚合后计算），代表书返回 `version_count`（该组在当前筛选下的可见版本数）；列表书籍均回填 `latest_version`（组内最新版的版本号） | `book:read` |
| POST | `/books` | 创建书籍 | `book:create` |
| GET | `/books/:id` | 书籍详情（含作者） | `book:read` |
| GET | `/books/slug/:slug` | 按 slug 查书籍 | `book:read` |
| GET | `/books/slug/:slug/access` | 服务端计算当前用户的对象级能力：`can_read/can_manage/can_edit_content/can_export/collaborator_role` | `book:read` + 登录 |
| PUT | `/books/:id` | 更新书籍（标题/简介/封面/状态/公开性/排序规则/章节前缀/阅读水印/`child_status_follow_parent` 新建子章节状态跟随父章节）；`slug` 仅当 `slug_editable=true` 时可改一次，改后自动置为不可改（复制出的书籍具备该资格）；`extra_info:[{type,label?,value}]` 为「更多信息」附加属性（整体替换，最多 20 项，空值项丢弃；`type` 取 github/gitlab/gitee/website/source/docs/demo（需 http(s) 链接）、email、author/license/version/isbn（文本）或 custom（需 `label`），在书籍详情页展示，随导出/导入与复制保留） | `book:update` |
| DELETE | `/books/:id` | 将书籍及当前章节移入 30 天回收站 | `book:delete` |
| GET | `/books/status-counts?scope=owned\|collaborating` | 当前用户创建或已接受协作书籍的状态统计 | `book:read` |
| POST | `/books/:id/view` | 可见书籍浏览计数 +1，并写入按日、来源聚合桶；可选 JSON `{referrer}`，只保存来源类别，不保存原始网址；不可见资源统一返回 404 | `book:read` |
| GET | `/books/:id/analytics?days=7\|30\|90\|180` | 书籍聚合分析：累计/周期浏览、上一周期增长、每日趋势、热门章节、来源类别、登录读者完成率、章节到达漏斗（`chapter_funnel`：按章节顺序的去重读者数）；仅 owner/admin，日聚合最多保留 180 天 | `book-analytics:read` |

书籍字段：`id, title, description, cover_image, slug, status(draft|in_progress|published|completed|archived), is_public, login_required, view_count, order_col(created_at|updated_at|title|view_count), order_dir(asc|desc), chapter_prefix, watermark_enabled, watermark_text, user, tags, created_at, updated_at`

> **书籍状态语义**：`draft` 草稿（不对外阅读）、`in_progress` 进行中、`published` 已发布（兼容既有数据）、`completed` 已完成、`archived` 已归档（从公开区域下线）。当 `is_public=true` 时，`in_progress / published / completed` 均属于可公开阅读状态；章节仍只使用 `draft / published / archived`。
>
> **可见性**：`is_public=false` 仅作者/协作者可见；`is_public=true` 且 `login_required=false` 所有访客可发现并阅读；`is_public=true` 且 `login_required=true` 仅登录用户可发现并阅读（未登录游客在列表、探索、搜索、标签中均看不到，直接访问也被拒绝）。作者/协作者/管理员不受 `login_required` 限制。

- 阅读水印默认关闭。创建或更新书籍时传 `watermark_enabled: true` 与自定义 `watermark_text`（去除首尾空白后最多 80 个字符）；开启时水印内容不能为空。关闭水印不会清除已经保存的自定义内容。

## 文档（章节，支持树形）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/:id/documents` | 可见书籍的文档树（不含正文）；未授权统一 404，普通读者/viewer 仅含已发布章节 | `document:read` |
| GET | `/books/:id/translations` | 同一翻译组（书籍 `trans_group` 相同且非空）内对当前用户可见的书籍 `items:[{slug,title,language,version,current}]`（少于两本返回空），供阅读页语言切换。**受「书籍多语言」插件守卫**（默认启用，禁用后 404、前端入口隐藏） | `book:read` |
| GET | `/books/:id/versions` | 同一版本组（书籍 `version_group` 相同且非空）内对当前用户可见的书籍 `items:[{slug,title,language,version,current}]`（少于两本返回空），供阅读页版本切换。**受「书籍版本」插件守卫**（默认启用，禁用后 404、前端入口隐藏） | `book:read` |
| GET | `/books/:id/versions/books?page&page_size` | 同一版本组内对当前用户可见的全部书籍（完整书籍对象，按版本号数字逐段比较、依站点 `book_versions_sort` 排序），分页返回 `{items,total,page,page_size}`，供列表「N 个版本」弹框。**受「书籍版本」插件守卫** | `book:read` |

> 「书籍多语言」（`book-translations`）与「书籍版本」（`book-versions`）为 feature 插件，默认启用；数据（`language`/`trans_group`/`version`/`version_group`）存于书籍字段，禁用只停用切换入口与这两个接口，不删数据。
| POST | `/books/:id/copy` | 复制可读书籍的元数据 + 章节到当前用户名下的**私有草稿**新书。JSON `{title?, mode:full\|custom, doc_ids?}`：`full` 按原结构与顺序复制全部章节；`custom` 仅复制 `doc_ids`（有序，即复制后顺序），父章节同在所选集合内则保留父子、否则升为顶层。返回 `{book, copied_documents}` | `book:create` |
| POST | `/books/:id/documents` | 创建文档（title 必填；slug 留空自动生成；parent_id 归属校验；成功后生成初始版本） | `document:create` |
| GET | `/books/:id/documents/slug/:slug` | 按 slug 查文档（含正文） | `document:read` |
| GET | `/documents/:id` | 文档详情（含正文） | `document:read` |
| PUT | `/documents/:id` | 更新（title/content/parent_id/sort_order/status/slug；防环校验）；改 `status` 时传 `cascade_status: true` 可把新状态一并应用到整棵子章节树；手动保存传 `create_revision: true` 与 `revision_reason: save|publish` 生成不可变版本 | `document:update` |
| DELETE | `/documents/:id` | 将文档及其子树作为同一批次移入 30 天回收站 | `document:delete` |
| POST | `/documents/:id/view` | 章节浏览计数 +1，并同步累加所属书籍的 `view_count` 及按日分析聚合；可选 JSON `{referrer}`；不可见返回 404 | `document:read` |
| GET | `/documents/:id/revisions` | 章节版本列表（分页，不含正文）；未授权统一 404 | `document-revision:read` |
| GET | `/documents/:id/revisions/:revisionId` | 版本详情（含正文）；未授权或版本不属于章节时统一 404 | `document-revision:read` |
| POST | `/documents/:id/revisions/:revisionId/restore` | 恢复标题、正文、发布状态与评论设置；自动保留恢复前及恢复后快照 | `document-revision:restore` |

文档字段：`id, book_id, parent_id, title, slug, content( markdown), user_id, sort_order, view_count, status, allow_comments(公开后允许评论，默认 true), created_at, updated_at, children`；创建/更新请求体同样接受 `allow_comments`

版本列表字段：`id, document_id, book_id, title, content_length, status, allow_comments, reason(create|save|publish|pre_restore|restore), author(仅公开字段), created_at`；详情额外返回 `content`。版本记录只新增、不提供修改接口，目录排序等结构调整不会生成版本。

> **协作（M14/M37）**：邀请初始为 `pending`，受邀用户明确接受成为 `accepted` 后才获得权限。accepted editor 拥有章节内容的增删改权限，书籍设置与删除仍限所有者/管理员；accepted viewer 可访问私有协作书籍及其已发布章节。pending/rejected 不得访问私有书籍，也不得出现在协作书籍、搜索或收藏的私有数据中。

## 回收站（M36）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/trash?type=book\|document&page=&page_size=` | 分页查看回收站；章节只列每个删除批次的根节点并返回子章节数 | `trash:read` |
| POST | `/trash/books/:id/restore` | 恢复书籍及该次随书删除的全部章节 | `trash:restore` + owner/admin |
| DELETE | `/trash/books/:id` | 永久删除书籍、章节、版本、评论、协作、互动和阅读数据 | `trash:delete` + owner/admin |
| POST | `/trash/documents/:id/restore` | 恢复同一删除批次的章节子树并保持父子关系；父章节在其他批次回收站时需先恢复父章节 | `trash:restore` + owner/admin/editor |
| DELETE | `/trash/documents/:id` | 永久删除同一批次的章节子树及其版本、评论和阅读数据 | `trash:delete` + owner/admin |

回收站条目字段：`type, id, title, slug, book_id, book_title, book_slug, owner_username, descendant_count, deleted_at, expires_at`。普通查询、公开页面、搜索、统计和管理列表均默认排除软删除内容。内容保留 30 天；访问回收站时会清理当前可见范围内已过期的条目。未授权读取、恢复或永久删除统一返回 404，避免泄露资源存在性。

## 协作者与邀请（M14/M37）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/:id/collaborators` | 协作者列表（含 user 摘要与 pending/accepted/rejected 状态）；所有者/管理员可查全部，已接受协作者只看已加入成员 | `collaborator:read` |
| POST | `/books/:id/collaborators` | 发送邀请 `{username, role: editor\|viewer}`；新邀请为 pending；已接受成员仅覆盖角色 | `collaborator:create` |
| DELETE | `/books/:id/collaborators/:userId` | 移除协作者；协作者可传自己的 userId 退出协作 | `collaborator:delete` |
| GET | `/collaboration/invitations` | 当前用户待确认的协作邀请 | `collaborator:read` |
| POST | `/collaboration/invitations/:id/accept` | 接受自己的待确认邀请，随后协作权限生效 | `collaborator:update` |
| POST | `/collaboration/invitations/:id/reject` | 拒绝自己的待确认邀请，不授予权限 | `collaborator:update` |

“我的书籍”协作视图使用 `GET /books?scope=collaborating`，仅返回当前用户已接受的协作书籍，并在每本书上返回 `collaborator_role`。原有 `mine=true` 与 `scope=owned` 均表示本人创建的书籍。

## 标签

> 标签为「标签系统」特性插件（默认启用）。插件禁用后本节全部端点与后台标签管理一并返回 404。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/tags?q=&limit=` | 标签列表（含 `icon_type`/`icon_value`/`book_count`，按计数降序） | `tag:read` |
| POST | `/tags` | 创建标签 `{ "name": "Go" }` | `tag:create` |
| DELETE | `/tags/:id` | 删除标签并解绑全部书籍 | `tag:delete` |
| GET | `/tags/:slug/books?page=` | 按标签查公开书籍（分页） | `tag:read` |
| GET | `/admin/tags?q=&page=&page_size=` | 后台标签管理：全部标签（含图标与总使用计数），分页 | `tag:manage` |
| POST | `/admin/tags` | 后台创建标签 `{name, icon_type?, icon_value?}`（icon_type: fa/image/svg） | `tag:manage` |
| PUT | `/admin/tags/:id` | 更新标签名称与图标（slug 保持不变） | `tag:manage` |
| DELETE | `/admin/tags/:id` | 删除标签（同 `/tags/:id`） | `tag:delete` |

- 书籍对象包含 `tags: [{ id, name, slug }]`；创建/更新书籍时请求体可传 `tags: ["Go", "后端"]`，服务端自动 find-or-create 并全量替换关联（单书最多 10 个）
- 列表过滤：`GET /books?tag=<slug>`
- 图标上传走通用 `POST /upload`（返回媒体地址），前端用通用 `IconPicker` / `ResourceIcon` 组件

## 上传

- `POST /upload`（multipart `file`，大小上限为当前用户的 `upload.max_mb` 权益，基础值即内容设置中的上传大小，默认 10MB，png/jpg/jpeg/gif/webp/svg/ico）：按存储配置写入 **local**（默认，返回 `/uploads/<name>` 相对地址，由 `/uploads/*` 静态服务）或 **qiniu**（表单上传，返回 `<CDN 域名>/<key>` 绝对地址）或 **s3**（S3 兼容对象存储 PutObject，返回对外访问地址或对象直链）；凭据存站点配置表（`storage_driver`、`qiniu_*`、`s3_*`），管理端经 `/storage` 维护

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/upload` | `multipart/form-data` 字段 `file`，仅图片（png/jpg/jpeg/gif/webp/svg/ico），≤ `upload.max_mb` 权益；返回 `{ url }`（如 `/uploads/xxx.png`） | `upload:create` |

## 评论

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/documents/:id/comments` | 可读章节的公开评论列表；用户仅返回公开资料字段 | `comment:read`（匿名语义） |
| POST | `/documents/:id/comments` | 向可读且开启评论的章节发表评论/回复 | `comment:create` |
| PUT | `/comments/:id` | 编辑自己的评论 | `comment:update` |
| DELETE | `/comments/:id` | 评论作者、书籍所有者或管理员删除评论 | `comment:delete` |

## 点赞 / 收藏（登录用户）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/books/:id/reactions` | 对当前可见书籍点赞或收藏，请求体 `{ "type": "like" \| "favorite" }`，重复请求幂等 | `reaction:create` |
| DELETE | `/books/:id/reactions?type=` | 取消（like / favorite） | `reaction:delete` |
| GET | `/books/:id/reactions/me` | 当前用户对该书的态度 + 全站计数 | `reaction:read` |
| GET | `/users/me/reactions?type=&page=` | 我的点赞/收藏列表；自动过滤当前已失去访问权的书籍 | `reaction:read` |

## 书籍关注（登录用户，「书籍关注」插件）

> `book-follow` 为 feature 插件（默认启用）。禁用后本节端点与 `/users/me/follows` 返回 404，前端关注入口/我的关注/通知偏好项隐藏，更新也不再通知。权限 `follow:*` 由插件动态注册。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/books/:id/follow` | 关注书籍（幂等），返回 `{following,count}` | `follow:create` |
| DELETE | `/books/:id/follow` | 取消关注，返回 `{following,count}` | `follow:delete` |
| GET | `/books/:id/follow/me` | 当前用户是否关注 + 关注数 | `follow:read` |
| GET | `/users/me/follows?page=&page_size=` | 我关注的书籍（分页，过滤已失去访问权的） | `follow:read` |

- 关注者在被关注书籍**发布新章节**（草稿→已发布）时收到 `book_update` 站内通知（作者本人除外），受用户「关注更新」通知偏好（`book_update`）开关控制。

## 用户成长等级（「成长等级」插件，默认关闭）

> `growth` 为 feature 插件（默认**关闭**，开关键 `growth_enabled`）。启用后建表、注册 `growth:*`/`experience:adjust` 权限并种子默认等级；禁用后本节端点与页面/入口一并停用（数据保留）。经验只由服务端权威事件产生（成就解锁奖励 `reward_xp`、首次读章节、管理员调整），流水不可变、`dedupe_key` 唯一保证幂等，等级由经验按 `min_xp` 阈值解析，升级写历史 + `growth` 通知。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/growth/settings` | 模块是否启用（不受插件守卫，禁用返回 `enabled:false`） | 公开 |
| GET | `/growth/levels` | 等级阶梯（active） | 公开 |
| GET | `/growth/leaderboard?period=all / week / month&page&page_size` | 经验排行榜：`all` 按累计经验，`week`/`month` 按近 7/30 天流水之和（含收回的负经验）；仅含公开成长资料、经验达到「最少上榜经验」的启用用户；管理员关闭排行榜时 404。响应 `{items:[{rank,xp,user,level}],total,page,page_size,period,min_xp,me?}`，登录时 `me` 为本人经验与名次（未公开也可见） | 公开 |
| GET | `/users/me/checkin?month=YYYY-MM` | 签到状态：`{enabled,today,checked_today,streak,longest_streak,total_days,streak_bonus_days,next_bonus_in,month,days[]}` | `growth:read` |
| POST | `/users/me/checkin` | 今日签到（幂等；同日重复返回 `already:true`）：发出 `checkin.created`，连续每满 N 天另发 `checkin.streak_milestone`（经验规则 `checkin.daily` / `checkin.streak_bonus`，成就指标 `checkin.*`）；响应含 `xp_awarded`、`milestone`；签到关闭时 404 | `growth:update` |
| GET | `/users/:username/growth` | 用户公开等级（用户隐藏则 `public:false`） | 公开 |
| GET | `/users/me/growth` | 我的成长（等级/经验/进度/下一级） | `growth:read` |
| GET | `/users/me/experience-events?page=` | 我的经验流水（分页） | `growth:read` |
| PUT | `/users/me/growth/display` | `{public}` 切换是否公开等级 | `growth:update` |
| GET | `/admin/growth/levels` | 全部等级（含归档） | `growth:manage` |
| POST/PUT/DELETE | `/admin/growth/levels[/:id]` | 等级增删改（等级 1 不可删、阈值恒 0；编号唯一）；`entitlements{key:value}` 为该等级的特权（未出现的键不设置，按键校验类型与范围，未知键 400） | `growth:manage` |
| POST | `/admin/growth/adjust` | `{user_id 或 username, xp, reason}` 人工加减经验（优先按 user_id；响应含 username 与最新经验/等级）（写审计，生成 adjustment 流水） | `experience:adjust` |
| GET | `/admin/growth/rules` | 经验规则列表，附 `stats_7d{rule_key:{count,xp}}` 近 7 天发放统计（按代码内的经验触发器目录补建缺失规则：原有阅读章节/发布章节/发表评论默认启用，其余业务活动如阅读时长、标注、建书、点赞、收到评论、账号安全等默认停用） | `growth:manage` |
| PUT | `/admin/growth/rules/:id` | 更新规则 `{base_xp,daily_cap,enabled}` | `growth:manage` |
| GET | `/admin/growth/events?user_id&rule_key&page&page_size` | 全站经验流水（倒序），可按用户/规则筛选，条目附 `user{id,username,nickname,avatar}` | `growth:manage` |
| GET/PUT | `/admin/growth/settings` | 成长设置 `{leaderboard_enabled, leaderboard_min_xp, checkin_enabled, checkin_streak_days}`（最少上榜经验 1–1000000，连续签到奖励周期 2–365 天；PUT 可只传部分字段）；公开站点配置 `GET /site` 同步下发 `growth_leaderboard_enabled` | `growth:manage` |

- 成就定义新增 `reward_xp`（默认 0）：解锁时给作者奖励经验（每 user+achievement 只结算一次）。
- **经验规则**（`ExperienceRule`）：固定事件（`reading.chapter`、`creation.chapter_published`、`community.comment`）的经验金额与**每人每日上限**由规则表配置，启用时种子默认规则；成就/管理员调整不走规则（金额分别为 reward_xp / 手工值）。
- 已接经验来源：首次读章节、章节发布（作者）、发表评论、成就解锁、管理员调整。公开主页头部显示等级徽标（用户可隐藏）。
- 后续（Phase 3+）：更多来源与创作/社区权威事件、`growth.*` 成就指标双向联动、赛季、排行榜、追溯补算。见 `user-level-system.md`。

## 会员（「会员」插件，默认关闭）

多个会员方案，每个方案可配置权益（见「权益」）与多档时长价格；用户同一时间持有一个方案：有效期内同方案续期顺延，有效期内更换方案从当前时间起按新方案计算（原方案剩余时长不保留）。会员有效期内作为**独占**权益来源（优先于成长等级，方案未配置的项回退基础值）。金额以最小货币单位（分）存储，货币由会员设置指定；启用「支付」插件后每档价格可在线购买（商品 `kind=membership`、`sku`=价格 ID，下单时方案须启用中，已付款订单即使方案随后归档也会开通）。方案名称/说明为可翻译资源（`membership_plan`），按请求语言回退。到期前 N 天（默认 3，0 不提醒）与到期后各通知一次。插件禁用后接口 404、会员权益不再生效，数据保留。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/membership/plans` | 启用中的方案 `items:[{id,name,description,icon_*,color,entitlements,prices:[{id,duration_days,price_cents,original_price_cents}]}]` + `currency` | 公开 |
| GET | `/users/me/membership` | 我的会员 `membership{plan,started_at,expires_at,active,days_left}`（无则 null，含最近一次已到期）+ 最近 20 条 `records` + `currency` | `membership:read` |
| GET | `/admin/membership/plans` | 全部方案（含归档）`items:[{plan{...,translations},active_members}]` | `membership:manage` |
| POST/PUT | `/admin/membership/plans[/:id]` | `{translations 或 name/description, icon_type, icon_value, color, status: active\|archived, sort_order, entitlements, prices:[{id?,duration_days,price_cents,original_price_cents}]}`；价格按请求整体替换（带 id 更新、新增、缺失删除），时长不可重复，售价 > 0，划线价为 0 或不低于售价；启用需有已发布的默认语言名称 | `membership:manage` |
| DELETE | `/admin/membership/plans/:id` | 仅可删除无人持有的方案（否则 409，请归档）；归档方案不能再开通，已有会员不受影响 | `membership:manage` |
| GET | `/admin/membership/members?q=&status=active\|expired&plan_id=&page=&page_size=` | 会员列表 `items:[{user,plan,started_at,expires_at,active}]` | `membership:manage` |
| POST | `/admin/membership/grant` | `{user_id, plan_id, days(1–3650), reason?}` 开通/续期/更换，返回 `{action: grant\|extend\|switch, expires_at,...}`，通知用户 | `membership:manage` |
| PUT | `/admin/membership/members/:user_id` | `{plan_id, expires_at(RFC3339，晚于当前且不超过 10 年), reason?}` 直接设置方案与到期时间 | `membership:manage` |
| POST | `/admin/membership/members/:user_id/revoke` | `{reason?}` 取消会员（立即失效，流水保留） | `membership:manage` |
| GET | `/admin/membership/records?user_id=&page=&page_size=` | 会员流水 `items:[{record{action,plan_name,days,prev_expires_at,expires_at,source,source_ref,reason,created_at},user,operator?}]` | `membership:manage` |
| GET/PUT | `/admin/membership/settings` | `{currency(ISO 4217), reminder_days(0–30)}`，PUT 可只传部分字段 | `membership:manage` |

## 支付（「支付」插件，默认关闭）

售卖其他插件经 `plugincore.RegisterProductProvider` 登记的商品（如会员：`kind=membership`，`sku`=价格 ID）。下单时快照商品标题、金额与履约数据；支付成功后回调提供者 `Fulfill`（按订单号幂等），之后的改价/归档不影响已下单订单。支付方式：线下转账（用户提交付款说明，管理员确认到账）、支付宝（电脑/手机网站支付，RSA2，仅 CNY）、微信支付（APIv3 Native 扫码，微信支付公钥模式，仅 CNY）、Stripe Checkout。回调与返回地址基于「站点访问地址」`site_url`。在线订单 2 小时有效，线下转账默认 72 小时；已取消/过期的订单若收到渠道支付成功回调仍按已支付入账。回调校验签名、金额、货币与支付方式，不符拒绝入账。`/site` 下发 `payment_channels`（当前可用的支付方式，仅支付插件自身使用）与中性开关 `checkout_enabled`（`plugincore.CheckoutEnabledKey`：是否可在线购买，商品所属插件的前端只看此键，结算入口为 `/pay/checkout?kind=&sku=`，前端约定见 `lib/commerce.ts`）。


**退款**：用户在支付后 `refund_request_days`（默认 7，0 为不开放）天内可申请退款，管理员审核（可下调金额、选择是否撤销商品）或驳回；管理员也可在订单上直接退款。支持部分退款（全额退完订单置为 `refunded`，部分退款仍为 `paid` 并记 `refunded_cents`）。
- 额度以订单上的占用额原子校验（条件更新），并发退款不会超额；退款单号作为渠道幂等键（支付宝 `out_request_no`、微信 `out_refund_no`、Stripe `Idempotency-Key`）。
- 渠道：支付宝 `alipay.trade.refund` + `alipay.trade.fastpay.refund.query`；微信 `/v3/refund/domestic/refunds`；Stripe `/v1/refunds`（按 PaymentIntent）；线下转账由管理员线下退回后确认即记为已退款。
- 发起时网络中断等结果未知的情况不会判为失败（避免重复退款），而是置为「退款中」由查询确认；渠道也无法确认时，管理员在商户后台核实后人工确认结果。受理中的退款在查看时（限频 5 秒）与巡检时查询。
- 退款成功后回调商品提供者 `Refund`（`plugincore.RefundEvent`，按退款单号幂等，失败由巡检重试）：会员在撤销时按比例扣回该订单开通的天数（扣完则会员结束）；付费内容按比例扣回作者净收益（平台抽成同比例退回，余额可为负），撤销时删除购买记录重新锁定。
- 已支付或已退款的订单不会被重复的支付通知重新置为已支付或再次履约。
- 通知：用户（退款成功、失败、驳回）、管理员（新的退款申请）、作者（收益扣回）。
| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/payment/products/:kind/:sku` | 结算页：`product{kind,sku,title,description,duration_days,amount_cents,currency,return_link}` + 该货币可用的 `channels` | `payment:order` |
| POST | `/payment/orders` | `{kind, sku, channel: offline\|alipay\|wechat\|stripe, mobile?}` 下单并发起支付，返回 `{order, action}`；`action.type`：`redirect`（`url` 跳转收银台）/ `qrcode`（`qr` 二维码 data URI）/ `offline`（`instructions`、`qr_image`）。同时待支付订单最多 10 个 | `payment:order` |
| GET | `/payment/orders/:no` | 订单详情（本人或管理员），含退款记录 `refunds[]`（退款中的会先向渠道查询）、`refundable_cents`、`can_request_refund` 或 `refund_blocked_reason`；待支付的在线订单会先向渠道主动查询一次（限频 5 秒，回调不可达时的兜底），待支付时附带继续支付的 `action` | `payment:order` |
| POST | `/payment/orders/:no/cancel` | 取消待支付订单 | `payment:order` |
| POST | `/payment/orders/:no/proof` | `{note}` 线下转账：提交付款说明 | `payment:order` |
| POST | `/payment/orders/:no/refund-request` | `{reason}` 申请退款（本人、已支付、在可申请期内、没有处理中的退款），申请剩余可退金额，等待审核 | `payment:order` |
| GET | `/users/me/orders?page=&page_size=` | 我的订单 | `payment:order` |
| POST | `/payment/notify/:channel` | 渠道异步通知（`alipay` / `wechat` / `stripe`），公开、以签名校验；不受插件开关限制 | 签名 |
| GET | `/admin/payment/orders?status=&channel=&q=&awaiting=1&unfulfilled=1&page=` | 全部订单（`awaiting` 待确认的线下转账，`unfulfilled` 已支付未履约）`items:[{order,user}]` | `payment:manage` |
| POST | `/admin/payment/orders/:no/confirm` | 确认线下转账到账（置为已支付并履约，写审计） | `payment:manage` |
| POST | `/admin/payment/orders/:no/cancel` | 取消待支付/已过期订单 | `payment:manage` |
| POST | `/admin/payment/orders/:no/fulfill` | 重试履约（已支付但履约失败；巡检也会自动重试） | `payment:manage` |
| POST | `/admin/payment/orders/:no/refunds` | `{amount_cents, reason, revoke}` 直接退款（可部分），随即向渠道发起 | `payment:manage` |
| GET | `/admin/payment/refunds?status=&q=&page=` | 退款与退款申请（待审核、退款中在前）`items:[{refund,user,order}]`、`requested` 待审核数 | `payment:manage` |
| POST | `/admin/payment/refunds/:id/approve` | `{amount_cents?, revoke, note?}` 通过申请（金额不超过申请金额）并向渠道发起 | `payment:manage` |
| POST | `/admin/payment/refunds/:id/reject` | `{note}` 驳回申请（释放额度并通知用户） | `payment:manage` |
| POST | `/admin/payment/refunds/:id/sync` | 立即向渠道查询退款中的退款 | `payment:manage` |
| POST | `/admin/payment/refunds/:id/resolve` | `{succeeded, note}` 人工确认退款中的退款结果（渠道无法确认时） | `payment:manage` |
| POST | `/admin/payment/refunds/:id/settle` | 重试退款成功后的商品回调（冲回财务/撤销商品） | `payment:manage` |
| GET/PUT | `/admin/payment/settings` | 各支付方式配置；密钥类字段（私钥、公钥、APIv3 密钥、Stripe 密钥）只写不读，GET 仅返回 `<字段>_set`，PUT 传空串表示不修改；保存前校验密钥格式。GET 另含 `notify_urls`、`available`、`site_url_set` | `payment:manage` |

## 发布审核（「发布审核」插件，默认关闭，Issue #87）

敏感词词典 + 发布前自动审查，经 `plugincore.RegisterPublishGuard` 接入核心发布路径：章节发布（含创建即发布、已发布章节修改标题/正文、级联发布、Markdown/ZIP 导入、网页采集）与书籍公开（含公开书籍修改标题/简介、ZIP 还原）。匹配为 Aho-Corasick，忽略大小写与全角半角，可选忽略词语中间插入的空白与符号。
- **未命中**：直接发布，记入「自动通过」（管理员可复审：确认无误，或驳回并撤回发布）。
- **命中**：拦截——章节保持未发布（已发布的改动会撤回为草稿）、书籍保持私有；响应中 `publish_held` 为说明；记录命中位置（字段、行、列、原文片段、上下文），通知作者与全部管理员。管理员通过 → 自动发布（章节触发首次发布事件）；驳回（需填写意见）→ 把命中位置与意见通知作者。作者修改后再次发布时同一对象复用记录，未再命中则直接发布。
- 默认管理员发布免审；可分别关闭章节/书籍审查。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/users/me/moderation-cases?page=` | 我的审核记录（待审核/已通过/已驳回，含 `hits` 与 `review_note`；不含未复审的自动通过） | `moderation:read` |
| GET | `/admin/moderation/cases?status=pending\|auto_passed\|handled\|approved\|rejected&kind=document\|book&ai=flagged\|safe&q=&page=` | 审核记录 `items:[{case{…,hits, ai_status, ai_verdict, ai_confidence, ai_categories, ai_reason, ai_error},user,book,doc_slug}]` + `pending` 待审核数、`ai_flagged`（AI 判定违规或不确定的未结记录数）、`ai_active`；`ai=flagged` 只看这些记录。`/users/me/moderation-cases` 不返回 AI 结论 | `moderation:manage` |
| GET | `/admin/moderation/cases/:id/content` | 对象当前文本 `fields` 与按当前词典重新计算的 `hits` | `moderation:manage` |
| POST | `/admin/moderation/cases/:id/approve` | 通过（待审核 → 发布；自动通过 → 确认），已处理的 409 | `moderation:manage` |
| POST | `/admin/moderation/cases/:id/reject` | `{note}` 驳回（必填意见；自动通过的撤回发布），通知作者 | `moderation:manage` |
| GET/POST | `/admin/moderation/words` | 列表 `?q=&category=&page=`（附 `categories`、`enabled_total`）；批量添加 `{words, category?}`（换行/逗号/顿号分隔，已存在跳过，单个 ≤50 字，一次 ≤5000），返回 `{added, skipped}` | `moderation:manage` |
| PUT/DELETE | `/admin/moderation/words/:id` | `{enabled?, category?}` / 删除 | `moderation:manage` |
| POST | `/admin/moderation/test` | `{text}` 用当前词典与设置试审，返回 `hits` | `moderation:manage` |
| GET/PUT | `/admin/moderation/settings` | `{scope_documents, scope_books, skip_noise, admin_exempt, notify_pass}`，PUT 可只传部分字段 | `moderation:manage` |
| GET/PUT | `/admin/moderation/ai-settings` | AI 辅助审核 `{settings{mode: off\|advise\|auto_approve, min_confidence(0.5–0.99，默认 0.9), screen_passed, policy(≤2000 字)}, ai_available}`，PUT 可只传部分字段。开启后每条审查记录保存提交时的内容快照，后台任务 `moderation.ai_review` 让模型结合上下文复核（命中位置作为参考，正文取前 2 万字，记为系统调用 `moderation.ai`）：待审核的都复核；`screen_passed` 开启时也复核自动通过的内容，疑似违规时标记并通知管理员（`notify.moderation.aiFlagged`），不自动撤回。`auto_approve` 模式下，待审核内容被判定安全且置信度达到 `min_confidence` 时自动通过并发布（复审人为系统，意见以「AI 复核通过」开头）；其余情况只作参考 | `moderation:manage` |
| POST | `/admin/moderation/cases/:id/ai-review` | 重新让 AI 复核一条未结记录 → 记录（`ai_status=queued`）；已处理 409，未开启 400 | `moderation:manage` |
| GET | `/admin/moderation/stream?ticket=` | 审核队列的实时变化（SSE）：`case`（一条记录的最新状态，含 AI 复核进度与结论）；连接建立时推 `ready`，25 秒心跳 | `moderation:manage` |

## 付费内容（「付费内容」插件，默认关闭）

作者为自己的书籍/章节定价（站点可关闭作者定价，仅管理员可设），读者购买后解锁全文；平台按比例抽成（下单时锁定），作者收益入账后申请提现，管理员线下打款确认。
- **内容门禁**：经 `plugincore.RegisterContentGate` 接入核心所有内容出口：章节接口未解锁时 `content` 为试读内容（按段落截取约 `preview_percent`%），并返回 `paywall{locked, book_id, doc_id, currency, discount_percent, book_price_cents, book_final_cents, chapter_price_cents, chapter_final_cents, free_tier, logged_in, upgrade_link}`；整本导出（PDF/EPUB/DOCX/Markdown）要求全部解锁；搜索摘要只取试读内容。作者、协作者与管理员不受限。
- **免费规则**：章节单独设为免费、按目录顺序前 `free_chapters` 章、未设置任何价格；读者权益 `content.free_all` 为开，或 `content.access_tier` ≥ 书籍 `free_tier`（>0）时全书免费；已购整本或该章节。
- **与会员/等级结合（权益，插件间无依赖）**：`content.access_tier`（内容访问等级）、`content.discount_percent`（购买折扣，最多 90%）、`content.free_all`（全部免费），可在会员方案/成长等级的权益编辑器中授予。
- **购买**：商品 `paid-book`（sku=书籍 ID，需设整本价）与 `paid-doc`（sku=章节 ID，需有效章节价）经支付插件下单；已可阅读、作者本人、免费章节不能购买；履约按订单号幂等，写入购买记录与作者收益流水（售价、抽成、净收益）并通知作者。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/paid/books/:id` | 书籍付费信息与当前读者状态 `{enabled, currency, book_price_cents, book_final_cents, chapter_price_cents, free_chapters, preview_percent, free_tier, discount_percent, purchased_book, can_read_all, locked_doc_ids[], upgrade_link, is_author}` | 可读该书 |
| GET/PUT | `/books/:id/paid-settings` | 付费设置 `{enabled, book_price_cents, chapter_price_cents, free_chapters, preview_percent(0–50), free_tier(0–100), docs:[{doc_id, free, price_cents}]}`（章节设置整体替换）；GET 另含各已发布章节的生效结果、货币、单价上限与抽成比例。仅作者或管理员 | `paid:use` |
| GET | `/users/me/purchases?page=` | 我购买的书籍与章节 | `paid:use` |
| GET | `/users/me/earnings?page=` | 收益：`balance_cents`（可提现）、累计售价/抽成/净收益、收益流水（sale / withdrawal / withdrawal_revert）、最近提现记录 | `paid:use` |
| POST | `/users/me/withdrawals` | `{amount_cents, account}` 申请提现（不低于最低金额、不超过余额、同时仅一笔处理中），申请即冻结 | `paid:use` |
| GET | `/admin/paid/sales?page=` | 销售流水（含作者、买家）与累计售价/抽成 | `paid:manage` |
| GET | `/admin/paid/withdrawals?status=&page=` | 提现申请（含收款信息、作者当前余额） | `paid:manage` |
| POST | `/admin/paid/withdrawals/:id/pay` / `reject` | 确认已线下打款 / 驳回（需原因，金额退回余额），通知作者 | `paid:manage` |
| GET/PUT | `/admin/paid/settings` | `{currency, commission_percent(0–90), min_withdrawal_cents, max_price_cents, allow_authors, upgrade_link(站内路径)}` | `paid:manage` |

## 书籍问答（「书籍问答」插件，默认关闭）

读者就某本书的内容提问：AI 只依据本书（读者有权阅读全文的已发布章节）作答并标注出处；也可在社区问答中向作者和其他读者提问。模型经核心「AI 服务」（`/admin/ai`）调用，插件不接触密钥。
- **索引**：已发布章节按 H2/H3 小节切分（锚点 `h-N` 与阅读页一致，过长小节按段落再切，约 900 字），关键词检索用中日韩单字+二元组与拉丁词的 BM25；配置了嵌入模型时后台任务 `qa.index` 为分块计算向量，检索改为「向量 + 关键词」各半的混合打分。内容变化（章节 ID/更新时间摘要）时提问前自动重建，未变化的分块复用已算好的向量。
- **作答**：标准模式检索 `top_k` 个片段后一次作答；Agent 模式（`mode=agent`）由模型调用 `search_book` / `read_section` / `get_toc` 工具，直到模型不再调用工具为止（不限轮数，不设整体超时；与之前完全相同的工具调用不重复执行，直接提示模型基于已有结果作答）。回答中的 `[n]` 映射为出处 `citations[]{n, doc_id, doc_slug, doc_title, heading, anchor, snippet}`，前端链接到 `/book/reader/<书>/<章节>#<anchor>`。划词提问（`selection` + `doc_id`）优先加入所在章节中包含选中文字的小节。
- **后台生成与调用链**：提问后立即返回 `status=running` 的记录，回答在后台生成（不受反向代理超时影响），读者订阅 `GET /qa/asks/:id/stream`（SSE）实时接收每一步，可取消；状态 running\|done\|failed\|canceled。每条记录含调用链 `trace[]`（按顺序：`context` 划词/当前章节上下文、`embed` 查询向量化、`retrieve` 检索命中、`model` 模型调用（模型、输入/输出 tokens、耗时、请求的工具及参数）、`tool` 工具执行（参数、检索方式、返回的小节））、合计 `calls`/`input_tokens`/`output_tokens`/`duration_ms`，以及与核心 AI 用量一致的 `trace_id`（可在「我的 AI 用量」查看同一链条的全部调用）。服务重启时遗留的进行中记录由巡检标记为中断。错误只返回面向读者的说明，不透出 AI 服务原始报错。
- **内容治理**：提问与回答登记为用户内容（`plugincore.RegisterUserContent`，类型 `qa_question` / `qa_answer`），纳入发布审核与内容举报。新内容先以待审核写入，经发布守卫（如敏感词审核插件，范围开关 `scope_ugc`）放行后才公开；被拦截时 `visibility=held`，只对本人与管理员可见，给作者/提问者的通知延后到审核通过时发送；驳回或举报下架后 `visibility=hidden`。回答数只统计公开回答；下架被采纳的回答时问题回到待解决。创建接口返回 `{question|answer, held, message}`。
- **额度（均为权益，可在成长等级/会员方案中提升或设为不限）**：每日 AI 提问次数 `qa.ai_daily`（基础 20）；其中深度模式另计 `qa.agent_daily`（基础 5，0 表示当前等级/会员不含深度模式）；另受核心每月 AI 用量 `ai.monthly_tokens` 约束。计入进行中与成功且实际调用了模型的提问（失败、取消、书中无相关内容不计），超出返回 429。
- **消耗**：每条问答记录 `calls`（模型调用次数）、`input_tokens`、`output_tokens`、`estimated`；每次模型调用另写入核心 AI 用量记录（功能 `qa.ask` / `qa.agent`，后台向量化为系统调用 `qa.index`）。内容门禁同样生效：未解锁的付费章节不参与检索。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/qa/books/:id/status` | `{ai_available, agent_available, vector_search, index?{chunks, embedded, indexed_at, embed_error}, quota?{used, limit, agent_used, agent_limit}（-1 不限）, can_reindex?}`；深度模式权益为 0 时 `agent_available=false` | 书籍可读 |
| POST | `/qa/books/:id/ask` | `{question(≤1000), selection?(≤2000), doc_id?, mode: rag\|agent}` → 立即返回 `status=running` 的问答记录，回答在后台生成；只填 `selection` 时视为「请解释这段内容」 | 登录 + `qa:use` |
| GET | `/qa/asks/:id` | 我的一条问答：状态、回答、出处、实时调用链 `trace[]` 与消耗 | 登录 + `qa:use` |
| GET | `/qa/asks/:id/stream?ticket=` | 实时进度（SSE，`?ticket=` 事件流凭证鉴权，见「事件流凭证」）：先推 `snapshot`（完整记录；进行中时含已生成的部分回答与 `answer_seq`），之后每新增一步推 `step` `{index, step, calls, input_tokens, output_tokens, estimated, duration_ms}`（客户端按 `index` 去重），回答文本逐段推 `delta` `{seq, text}`，某轮以工具调用结束时推 `reset` `{seq}` 清空临时文本（均按 `seq` 去重），结束推 `done`（最终记录）后关闭；25 秒心跳。消费过慢时服务端断开，EventSource 自动重连并重新获得快照 | 登录 + `qa:use` |
| POST | `/qa/asks/:id/cancel` | 取消进行中的问答（已产生的调用照常记入 AI 用量）；已结束返回 409 | 登录 + `qa:use` |
| GET | `/qa/books/:id/asks?page=` | 我在本书的 AI 问答记录（新→旧，含进行中/失败/已取消，含调用链） | 登录 + `qa:use` |
| GET | `/qa/me/asks?page=&book_id=` | 我在全部书籍的 AI 问答记录 `items[]{ask, book{id,slug,title}, book_available}`（含消耗） | 登录 + `qa:use` |
| DELETE | `/qa/asks/:id` | 删除自己的一条 AI 问答记录（进行中的先取消；不退还当日次数） | 登录 + `qa:use` |
| GET | `/qa/me/questions?page=` | 我在社区的提问 `items[]{question, book, book_available}` | 登录 + `qa:use` |
| GET | `/qa/me/quota` | 今日额度 `{used, limit, agent_used, agent_limit}` | 登录 + `qa:use` |
| GET | `/qa/books/:id/insights?days=7\|30\|90` | 作者洞察（默认 30 天）：`{days, include_asks, semantic, stats{ai_asks?, askers?, gaps?, community, open}, topics[], gaps[], chapters[]{id,title,slug,count}, unanswered[]{id,title,created_at}}`；`topics[]`/`gaps[]` 为相近问题的归类 `{question（代表问题）, count, asks, community, gaps, samples[], chapters[], last_at}`。相近问题按问题向量余弦相似度 ≥ 0.82 归类（没有向量时按关键词重合度 ≥ 0.5）；AI 提问没有出处即视为书中答不上来（`gaps`），答不上来的占一半及以上的类列入内容缺口。读者的 AI 提问只以匿名文字出现，作者与协作者自己的提问不计入；`insights_asks` 关闭时只统计社区提问。问题向量由巡检与打开本页时在后台补算（系统调用，功能 `qa.insights`），提问流程本身不增加模型调用。每周有新提问时给书籍作者发一次站内通知（`notify.qa.weeklyInsights`，可由 `insights_digest` 关闭） | 作者/协作者 + 权益 `qa.insights` |
| POST | `/qa/books/:id/reindex` | 作者/协作者/管理员立即重建索引（向量在后台计算） | 登录 + `qa:use` |
| GET | `/qa/books/:id/questions?filter=all\|open\|resolved&q=&page=` | 社区问题列表（已解决在前，按更新时间排序），含提问者 `user` | 书籍可读 |
| POST | `/qa/books/:id/questions` | `{title(≤200), body?, doc_id?, selection?, ask_id?}` 提问（先审查，返回 `{question, held, message}`）；`ask_id` 附上自己的 AI 回答作参考；公开后通知作者 | 登录 + `qa:use` |
| GET | `/qa/questions/:id` | 问题详情 + `answers[]{answer, user, accepted, is_author, can_delete}`（采纳的在最前）+ `can_accept`、`can_manage` | 书籍可读 |
| DELETE | `/qa/questions/:id` | 提问者、作者/协作者或管理员删除问题（连同回答） | 登录 + `qa:use` |
| POST | `/qa/questions/:id/answers` | `{body(≤10000)}` 回答（问题须已公开；先审查，返回 `{answer, held, message}`）；公开后通知提问者 | 登录 + `qa:use` |
| POST | `/qa/answers/:id/accept` | 提问者或作者采纳（再次调用取消），问题变为已解决；通知回答者 | 登录 + `qa:use` |
| DELETE | `/qa/answers/:id` | 回答者、作者/协作者或管理员删除回答（删除被采纳的回答时问题回到待解决） | 登录 + `qa:use` |
| GET/PUT | `/admin/qa/settings` | `{ai_enabled, agent_enabled, top_k(3–12), trace_retention_days(0 永久或 7–3650), semantic_search, insights_asks, insights_digest}`（GET 另返回 `semantic{public_books, indexed_books}`），PUT 可只传部分字段；超过保留天数的已结束问答由巡检清空调用链详情（`trace`），问题、回答与用量合计保留；另返回 `ai_chat_available`、`ai_embed_available` | 管理员 + `qa:manage` |
| POST | `/admin/qa/semantic/reindex` | 立即为索引缺失或内容已变化的公开书籍排队重建（任务 `qa.reindex`）→ `{queued}`；未开启或未配置嵌入模型时 400 | 管理员 + `qa:manage` |

## 整本 AI 翻译（「书籍多语言」插件）

作者（可编辑原书内容的用户）用 AI 把整本书翻译为新的语言版本，或把原书的新增与修改同步到已有译本。模型经核心「AI 服务」调用。
- **新建译本**：新建一本私有草稿书（沿用原书的导出、水印、标签等配置，`language` 为目标语言名称），与原书同一翻译分组（原书未分组时以其访问路径作为 `trans_group`），阅读页即可切换语言。后台先翻译目录（书名、简介、全部章节标题，一次调用返回 JSON 数组，解析失败时逐条翻译）并按原书结构建好全部章节（草稿，访问路径与原书一致），再逐章翻译正文：正文按空行切为不超过约 3000 字的片段逐段调用（单个段落不拆开），只由代码块组成的片段原样保留、不发给模型也不计字数。译文写入译本章节并记录版本（`reason=ai_translate`）；已发布的章节被更新时交发布守卫审查。
- **同步**：`target_book_id` 指定译本时，只翻译原文修改过（标题 + 正文 + 外链的摘要与上次翻译时不同）或新增的章节；修改过的章节重新翻译标题并更新内容，新增章节按目录位置建立。没有变化时返回 400。
- **术语表**：按原书 + 目标语言保存，翻译时写入系统提示要求统一采用；另可填写对整本书生效的翻译要求（同步时沿用）。
- **后台执行**：不设整体超时，可暂停/继续（暂停时当前章节未完成的部分不保存，继续时重新翻译该章）；某章调用失败时记为失败并继续下一章，可重试失败的章节；本月翻译字数或 AI 用量不足时任务暂停并说明原因。服务重启时遗留的进行中任务由巡检标记为已暂停。完成后站内通知发起人（`notify.bookTranslate.done`）。
- **计量与权益**：每次调用写入 AI 用量记录（功能 `translate.book`，按原文字符数记 `characters`，与写作台翻译一起计入每月翻译字数 `translate.monthly_chars`；配置了 AI 服务时该权益即可用），另受每月 AI 用量约束。能否使用整本 AI 翻译为开关权益 `translate.ai_book`（基础为开，可设为会员专享）。开始前按需要翻译的原文字数判定本月剩余额度，不足返回 429。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/:id/ai-translate` | `{available, allowed, chars_left(-1 不限), source{chapters, chars, language}, targets[]{book{id,slug,title,language,status}, target_lang, target_label, changed, added, chars, last_job}, jobs[]（最近 20 个）}` | 登录 + `booktrans:ai`，可编辑原书 |
| GET/PUT | `/books/:id/ai-translate/glossary?lang=` | 术语表；PUT `{lang, terms:[{source, target}]}` 整体替换（最多 500 条，各不超过 200 字，原文不可重复；原文与译文都为空的行忽略） | 同上 |
| POST | `/books/:id/ai-translate/jobs` | 新建译本 `{target_lang, target_label, title?, instructions?(≤1000)}`（`title` 为空时由 AI 翻译原书名），或同步 `{target_book_id, instructions?}` → `{job, items}`，任务立即开始 | 同上；同步另需可编辑译本 |
| GET | `/ai-translate/jobs/:id` | `{job{id, mode: full\|sync, stage: outline\|content\|done, status: running\|paused\|done\|failed, total, done, failed, chars, input_tokens, output_tokens, estimated, trace_id, error, ...}, items[]{id, source_doc_id, target_doc_id, ord, depth, title, target_title, status: pending\|running\|done\|failed, chars, input_tokens, output_tokens, duration_ms, error}}` | 发起人或管理员 |
| GET | `/ai-translate/jobs/:id/stream?ticket=` | 进度（SSE，事件流凭证鉴权）：先推 `snapshot {job, items, current?{item_id, seq, text}}`，之后推 `job`（任务合计）、`item`（章节状态）、`reset {item_id, seq}`（开始翻译某章）、`delta {item_id, seq, text}`（当前章节译文片段，按 `seq` 去重），结束或暂停时推 `done {job, items}` 后关闭；25 秒心跳 | 发起人或管理员 |
| POST | `/ai-translate/jobs/:id/pause` | 暂停进行中的任务（不在进行中返回 409） | 发起人或管理员 |
| POST | `/ai-translate/jobs/:id/resume` | 继续已暂停的任务（本月翻译字数已用完时 429） | 发起人或管理员 |
| POST | `/ai-translate/jobs/:id/retry` | 重新翻译失败的章节（没有失败章节时 400，进行中时 409） | 发起人或管理员 |

## 章节导读（「章节导读」插件，默认关闭）

AI 为章节生成阅读前导读（一两段）与本章要点，为书籍生成全书概览（Markdown），分别显示在阅读页正文上方与书籍详情页「关于这本书」下方。模型经核心「AI 服务」调用。
- **生成**：经任务队列一章一个任务（`chapterguide.chapter` / `chapterguide.overview`，不设超时）。章节正文取前 3 万字交给模型；概览基于已发布章节（目录顺序）的导读，没有导读的章节取开头约 600 字。内容（标题 + 正文）与上次生成时相同则复用，不调用模型；作者编辑过的导读/概览不被批量生成或自动更新覆盖，只有强制重新生成（`force`）才会替换。没有正文的章节不生成。
- **自动模式**（书籍设置）：章节首次发布（含创建即发布）立即排队生成；已发布章节标题或正文修改后约 90 秒排队（连续保存只触发一次），服务重启丢失的延迟由巡检补上。
- **读者可见性**：只显示已发布章节的导读；付费内容门禁未解锁的章节不显示（避免泄露正文），作者与协作者始终可见。
- **费用与权益**：管理员设置费用承担方——`author`（默认，用量记在触发生成的作者名下，计入其每月 AI 用量）或 `site`（记为系统调用）；AI 用量功能为 `chapterguide.chapter` / `chapterguide.overview`。每月生成次数为权益 `chapterguide.monthly`（基础 200，实际调用了模型的才计，复用不计），批量生成前按需要的章数判定，不足返回 429。
- **事件流**：管理页订阅本书导读状态变化（排队、生成中、完成、失败），不轮询。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/chapter-guides/docs/:id` | 读者：`{guide: {summary, points[], generated_at, edited} \| null}` | 书籍可读 |
| GET | `/chapter-guides/books/:id/overview` | 读者：`{overview: {content, generated_at} \| null}` | 书籍可读 |
| GET | `/chapter-guides/books/:id` | 管理：`{available, auto_generate, cost_bearer, quota{limit(-1 不限), used}, chapters[]{doc{id,title,slug,status,depth,empty}, guide{doc_id, summary, points, status: queued\|generating\|ready\|failed\|skipped, error, edited, stale, input_tokens, output_tokens, generated_at}\|null}, overview\|null}`（`stale` 为生成后内容已修改） | 登录 + `chapterguide:use`，可编辑该书 |
| PUT | `/chapter-guides/books/:id/settings` | `{auto_generate}` | 同上 |
| POST | `/chapter-guides/books/:id/generate` | `{scope: missing\|ids, doc_ids?, force?}` → `{queued}`；`missing` 为没有导读或已过期（未经编辑）的有正文章节；没有需要生成的返回 400 | 同上 |
| POST | `/chapter-guides/books/:id/overview/generate` | `{force?}`；正在生成时 409 | 同上 |
| PUT | `/chapter-guides/books/:id/overview` | `{content(≤1 万字)}` 编辑概览（视为与当前内容一致）；清空即删除 | 同上 |
| PUT | `/chapter-guides/docs/:id` | `{summary(≤2000), points[](≤12 条，各 ≤300)}` 编辑导读（视为与当前内容一致） | 同上 |
| DELETE | `/chapter-guides/docs/:id` | 删除导读（进行中 409） | 同上 |
| GET | `/chapter-guides/books/:id/stream?ticket=` | SSE：`guide`（章节导读，删除时 summary 为空且无状态）、`overview`（删除时为 null）；连接建立时推 `ready`，25 秒心跳 | 同上 |
| GET/PUT | `/admin/chapter-guides/settings` | `{cost_bearer: author\|site, ai_available}` | 管理员 + `chapterguide:manage` |

## AI 写作助手（「AI 写作助手」插件，默认关闭）

写作台中对选中的文字续写、改写、润色、扩写、精简，为章节生成大纲或摘要，或按作者的自定义要求处理。模型经核心「AI 服务」（`/admin/ai`）以流式接口调用，插件不接触密钥；只有能编辑该书内容的用户（作者、协作者、管理员）可用。
- **动作** `action`：`continue`（续写，`text` 为光标前的上文，只取末尾 6000 字）、`rewrite`、`polish`、`expand`、`shorten`、`outline`、`summary`（前端在未选中文字时传整章内容）、`custom`（`instruction` 必填）。其他动作的 `instruction` 为可选的附加要求（≤500 字）。`before`/`after` 为紧邻的上下文，只取前文末尾 3000 字、后文开头 1000 字供模型参考；处理对象本身不截断（上限 5 万字）。
- **后台生成**：创建后立即返回 `status=running` 的任务，结果在后台生成（不设整体超时，可随时取消，已生成的部分保留）；订阅 `GET /ai-writer/tasks/:id/stream` 实时接收文本。状态 running\|done\|failed\|canceled；服务重启时遗留的进行中任务由巡检标记为中断。错误只返回面向作者的说明。
- **采纳**：写作台对比原文与结果后替换选区或插入到下方（由前端写入正文，生成期间原文被修改时按原文重新定位），再调用 `adopt` 记录采纳方式。
- **额度（权益，可在成长等级/会员方案中提升或设为不限）**：每月使用次数 `aiwriter.monthly_uses`（基础 100，0 表示当前等级/会员不含写作助手）；计入进行中、已完成以及取消/失败前已生成内容的任务，超出返回 429。另受核心每月 AI 用量 `ai.monthly_tokens` 约束。
- **消耗**：每次调用写入核心 AI 用量记录（功能 `aiwriter.<action>`，关联章节或书籍，`trace_id` 与任务一致）；任务记录 `model`、`input_tokens`、`output_tokens`、`estimated`、`duration_ms`。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/ai-writer/status` | `{available, quota{limit(-1 不限), used}}` | 登录 + `aiwriter:use` |
| POST | `/ai-writer/tasks` | `{book_id, doc_id?, action, text, before?, after?, instruction?}` → 立即返回 `status=running` 的任务 | 登录 + `aiwriter:use`，可编辑该书 |
| GET | `/ai-writer/tasks?book_id=&doc_id=&page=&page_size=` | 我的写作助手记录（新→旧） | 登录 + `aiwriter:use` |
| GET | `/ai-writer/tasks/:id` | 我的一条任务 `{id, book_id, doc_id, action, instruction, input, result, status, error, trace_id, model, input_tokens, output_tokens, estimated, duration_ms, adopted: ""\|replace\|insert, adopted_at, created_at}` | 登录 + `aiwriter:use` |
| GET | `/ai-writer/tasks/:id/stream?ticket=` | 实时生成（SSE，`?ticket=` 事件流凭证鉴权）：先推 `snapshot`（进行中时 `result` 为已生成的部分，`result_seq` 为其包含的片段序号），之后逐段推 `delta` `{seq, text}`（按 `seq` 去重），结束推 `done`（最终记录）后关闭；25 秒心跳 | 登录 + `aiwriter:use` |
| POST | `/ai-writer/tasks/:id/cancel` | 取消进行中的任务；已结束返回 409 | 登录 + `aiwriter:use` |
| POST | `/ai-writer/tasks/:id/adopt` | `{mode: replace\|insert}` 记录已采纳（进行中或没有结果时 409） | 登录 + `aiwriter:use` |
| DELETE | `/ai-writer/tasks/:id` | 删除一条记录（进行中的需先取消，返回 409；不退还次数，AI 用量记录保留） | 登录 + `aiwriter:use` |

## 语义搜索与相关推荐

核心只经 `plugincore.RegisterSemanticProvider` 询问插件登记的语义检索能力（目前由「书籍问答」插件的「全站语义搜索与相关推荐」提供，管理员开启且配置了嵌入模型时可用），返回的章节与书籍再按当前用户的可见性与内容门禁过滤；没有可用能力时各接口返回空列表。
- **索引**：开启后为所有公开书籍建立问答分块的向量索引（巡检补齐；公开书籍的章节发布或修改后约 2 分钟重建，连续修改只重建一次），并为每本书、每个章节保存分块向量的均值。向量化为系统调用（功能 `qa.index`）。
- **检索**：先用书籍向量选出最相关的 20 本读者可读的书，再在这些书中按分块相似度取每章最相关的一处（未解锁的付费章节不参与）。搜索词的向量化为系统调用（功能 `qa.search`），相同搜索词复用向量。
- **相关推荐**：相关书籍比较书籍向量（不含同一作品的其他语言/版本，只推荐公开书籍）；相关章节先选相关的书（含本书），再比较章节向量。相似度低于 0.2 的不推荐。书籍详情页优先展示相关书籍，没有时回退到同标签书籍。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/search/semantic?q=&book=` | `{available, items[]{id, book_id, book_slug, book_title, doc_slug, title, heading, anchor, excerpt, score}, error?}`（`q` ≤ 100 字；`book` 为书籍访问路径时限定在该书）；搜索页在关键词结果之后单独加载，只显示关键词结果中没有的章节 | 公开（按可见性过滤） |
| GET | `/books/:id/related?limit=` | 内容相近的公开书籍 `{items: Book[]}`（默认 3，最多 12） | 书籍可读 |
| GET | `/documents/:id/related?limit=` | 内容相近的已发布章节 `{items[]{id, title, slug, book_id, book_slug, book_title}}`（默认 5，最多 12；只含标题，付费章节同样可以推荐） | 章节可读 |

## 站内通知（登录用户）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/notifications?page=&per_page=&unread=true` | 当前用户通知（ newest 在前）+ `unread_count` | `notification:read` |
| POST | `/notifications/read` | 标记已读：`{ids:[]}` 或 `{all:true}`，返回最新 `unread_count` | `notification:update` |
| GET | `/notifications/stream` | SSE 实时流（`?ticket=` 事件流凭证鉴权）：连接即推 `{"unread_count":n}`，新通知实时推送；25s 心跳。不接受 URL 中的登录令牌 | `notification:read` |
| POST | `/stream-tickets` | 签发事件流凭证 `{ticket, expires_in:60}`：EventSource 无法携带请求头，事件流接口（站内通知、问答进度、插件安装日志）以 `?ticket=` 鉴权，不接受 URL 中的登录令牌。凭证为 HMAC 签名的用户与过期时间，60 秒内用于建立连接（已建立的连接不受影响），不能当作 API 令牌；无状态，多实例可用。前端在连接被拒时自动换新凭证重连 | 登录 |

- 通知类型：`comment`（评论/回复）、`reaction`（点赞/收藏）、`collaboration`（协作邀请）、`moderation`（举报处理结果）、`achievement`（成就解锁/授予）、`system`（升级完成等）
- `payload` 为 JSON 对象，含 `link`（点击跳转地址）等扩展字段
- 多语言：`payload.i18n = {key, params}`（如 `notify.comment.chapter` + `{user, chapter}`），前端按界面语言渲染；`title` 为站点默认语言的兜底文案。通知邮件按收件人偏好语言（`preferred_locale`）渲染标题与正文固定文案，管理员在语言包中发布的同键翻译优先。模板由核心与插件在服务端 `internal/i18ntext` 登记，须与前端字典同键同文（有测试校验）。历史通知由一次性后台任务按模板反解析补上 `i18n`，无法识别的保持原标题
- 偏好语言：注册（含第三方登录首次注册、安装向导创建管理员）时，把当前请求的界面语言（匹配已启用的站点语言）记为用户偏好语言 `preferred_locale`；请求没有任何语言信号时不写入，继续跟随站点默认语言
- 系统邮件（邮箱激活、找回密码）同样多语言：收件人设置了偏好语言时按偏好，否则按触发请求的界面语言（`?locale` / `X-KnowForge-Locale` / Cookie / `Accept-Language`），最后回退站点默认语言；邮件文案键为服务端专用的 `email.*`，管理员可在语言包中发布同键翻译覆盖
- 触发规则：他人评论你的章节/回复你的评论、他人点赞/收藏你的书（重复操作不重复通知）、服务启动检测到版本变化时通知管理员

## 内容举报（登录用户）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/reports` | 举报当前有权查看的内容：`{target_type:"book"\|"document"\|"comment"\|插件登记的用户内容类型（如 qa_question、qa_answer）,target_id,reason,description?}`；原因支持 `spam/harassment/copyright/illegal/misleading/other`；同一用户对同一目标只能存在一条待处理举报 | `report:create` |

普通用户只能提交举报，无法读取队列或其他举报人的信息。目标不可见或不存在时统一返回 404；补充说明最多 1000 字。

## 导入导出（M16 / M46）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/:id/export?format=markdown` | 导出书籍为 zip：`book.md`（front-matter：标题/简介/slug/状态/公开/排序/章节前缀/封面/标签）+ `chapters/<序号>-<slug>.md`（front-matter：标题/slug/排序/状态/父章节/评论开关 + 正文）+ `images/`（本站 `/uploads` 图片随包携带并改写为相对引用，外链保持原样） | `book:export` |
| GET | `/books/:id/export/epub?style=author\|mine` | 导出书籍为 EPUB 电子书（纯服务端 Go 生成，无需插件）：含封面页、EPUB3 目录(nav)+EPUB2 目录(ncx)、逐章 XHTML（GFM Markdown 渲染）、样式表与内嵌 `/uploads` 图片。鉴权同 PDF；`style` 复用导出样式（字号/代码配色/是否含封面与目录）；开启水印时每章附水印文本。作者/协作者可导草稿章节，其余仅已发布章节 | `book:read`（匿名可导开放的公开书） |
| GET | `/books/:id/export/docx?style=author\|mine` | 导出书籍为 Word 文档（纯服务端 Go 生成 OOXML，无需插件）：GFM Markdown 渲染为标题样式(Heading1-6)/段落/加粗斜体删除线/内联与块代码/引用/有序无序列表/表格/分割线，内嵌本站上传的栅格图片（png/jpg/gif，按原始尺寸缩放至正文宽度；svg/webp 等退化为替代文本）。鉴权同 EPUB；`style` 复用导出样式（页面尺寸/页边距/字号/代码配色/是否含封面与简易目录）；开启水印时每章附水印文本 | `book:read`（匿名可导开放的公开书） |
| GET | `/books/:id/export/pdf?style=author\|mine` | 通过无头浏览器插件（chrome-headless-shell）导出 PDF。鉴权：作者/协作者/管理员始终可导；否则要求书籍公开、处于可阅读状态且 `export_enabled`，未登录游客还需 `guest_export_enabled`。`style=author`（仅当作者 `export_style_shared`）用作者导出样式，否则用请求者样式；水印始终取自作者设置。每页底部渲染页脚（书籍配置 > 导出者个人配置 > 默认 `Powered by <站点名>`）。未安装插件返回 400 | `book:read`（匿名可导开放的公开书） |
| GET | `/users/me/export/books?ids=1,2,3` | 批量导出：将本人（作者本人或编辑协作者，不走管理员越权）的多本书各自打包为独立 markdown zip，合并进一个外层 zip（每本为自包含的 `<slug>.zip`，可单独重新导入，往返无损）+ `manifest.txt`。`ids` 逗号分隔（单次上限 100 本），省略则导出全部自有书籍；无可导出书籍返回 403 | `book:export` |
| GET | `/users/me/exports?page=&page_size=` | 我的导出历史：本人导出过的书籍记录（markdown/pdf/docx/epub/zip 各类导出成功时各记一条），按时间倒序分页；`book` 字段在原书仍存在时返回（供封面/跳转），已删除的书仅保留标题快照 | `book:export` |
| POST | `/import` | multipart 上传 `file`（ZIP，≤64MB），可选 `title`；源文件以 0600 权限私有保存并返回 `202` 和 `task`，后台还原元数据、标签、章节树和图片；slug 冲突自动追加 `-imported-N`。安全限制：≤500 文件、解压总量 ≤64MB、拒绝绝对路径与 `..` 路径；ZIP 不含 `book.md` 时按普通 Markdown 目录导入（同 `/import/markdown`，书名默认取 ZIP 文件名） | `book:import` |
| POST | `/books/:id/cleanup/localize-images` | 外链图片本地化：下载章节中引用的外部 http(s) 图片（跳过站点与存储自身域名；经 SSRF 防护客户端，拒绝内网地址，单张不超过上传大小权益，按类型/内容识别图片）并存入当前存储驱动、改写引用；改动的章节生成版本记录。有任务队列时返回 `202` 与 `task`（结果 `{localized,failed,docs_changed,limit_reached,failures[]}`，单次最多 500 张） | `book:update` + 编辑权 |
| POST | `/import/markdown` | multipart `files[]`：多个 `.md`/`.markdown`（可附带其引用的图片，单个 md ≤5MB）或一个 Markdown 目录 ZIP；可选 `title`、`publish=true`（章节直接发布，默认草稿）。目录 → 章节层级（README/index 作目录正文）、名称自然排序（支持 `01-` 前缀）；标题取 front-matter `title` → 首行一级标题 → 文件名；相对路径图片上传到当前存储驱动并改写引用；指向包内其他 `.md` 的链接改写为阅读页链接；隐藏文件与 `__MACOSX` 忽略。新书为私有草稿 | `book:import` |
| POST | `/books/:id/documents/import-markdown` | 同上的 `files[]`，可选 `parent_id`（挂到该章节下，否则为第一级），导入为本书章节（状态按书籍默认规则或 front-matter `status`），返回 `{imported_doc}`；需书籍编辑权 | `document:create` |
| POST | `/import/pdf` | multipart 上传 `file`（PDF，≤64MB），可选 `title`；文件以 0600 权限私有保存后返回 `202` 和 `task`，后台根据文本坐标、字号和字体样式重建 Markdown 标题、段落、列表及代码块，移除重复页眉页脚并修正双栏阅读顺序；结果固定为私有草稿。扫描版 PDF 需预先 OCR | `book:import` |
| POST | `/books/:id/import/pdf` | multipart 上传 `file`（PDF，≤64MB）及 `mode=append\|replace`，仅书籍 owner/admin 可用；返回 `202` 和 `task` 后后台执行。`append` 将新解析的 Markdown 章节以草稿追加到目录末尾；`replace` 在同一事务内清理旧章节及关联状态后写入新草稿章节，并将书籍转为私有草稿。解析失败不会改动旧数据 | `book:import` |
| GET | `/tasks/:id` | 查询当前用户自己的后台任务状态；成功时返回解密后的 `result`，等待/执行/重试/失败状态返回进度和有限错误信息；无法枚举或读取他人的任务，任务载荷永不返回 | 登录用户 |
| POST | `/import/web` | JSON `{url,title?,render_mode?,include_source?}`（`include_source` 默认 false，为真则正文末尾附来源链接），`render_mode` 为 `auto`（默认）、`static` 或 `browser`；自动模式先静态抓取，检测到 SPA 空壳或正文不足时使用无头浏览器插件执行 JavaScript；`browser` 模式及自动模式的浏览器回退均依赖已安装的无头浏览器插件，未安装时 `browser` 直接报错、`auto` 退回静态；正文转为 Markdown 并将相对链接补全；结果固定为私有草稿 | `book:import` |
| GET | `/import/browser-available` | 无头浏览器插件是否已安装（`{available}`），供前端联动禁用「浏览器渲染」采集模式 | `book:import` |
| POST | `/books/:id/documents/import-web` | JSON `{url,title?,render_mode?,include_source?,parent_id?,sort_order?}`；复用网页正文提取与 SPA 渲染，剔除页头、页脚、导航、侧栏、广告、分享、评论、相关推荐与弹窗，直接在可编辑书籍内创建草稿章节。`include_source`（默认 false）为真时在正文末尾附加「来源：原始网页」链接 | `document:create` |
| POST | `/import/web-content` | JSON `{url,render_mode?,include_source?}`；复用同款网页正文提取，**不建文档**，返回 `{title,markdown,source_url,render_mode}`（`include_source` 默认 false，为真则 markdown 末尾附来源链接），供写作编辑器「采集网页」插入到当前章节光标处 | `document:create` |
| POST | `/books/:id/documents/copy` | JSON `{target_book_id, doc_ids:[...]}`；将选中章节**含各自子章节树**复制到目标书籍（需对目标书有编辑权），保持父子结构，顶层追加到目标目录末尾，复制为草稿。返回 `{copied_documents,target_book_id,target_slug}` | `document:create`（+ 目标书编辑权） |

> 安全边界：网页导入只允许 HTTP(S)，拒绝 localhost、内网、回环及链路本地地址；重定向和浏览器发起的子资源请求也执行同一校验。动态网页渲染依赖后台「无头浏览器」插件（与 PDF 导出共用同一 chrome-headless-shell），未安装则浏览器渲染不可用。所有导入章节都会生成 `create` 初始版本。PDF/ZIP 后台任务成功后立即删除源文件；最终失败任务保留源文件以供重试，超过 30 天由启动清理回收。

### 内容采集插件（`content-collect`，含单页/整站两个子开关）

> `/import/web`、`/import/web-content`、`/books/:id/documents/import-web` 均归入本插件：插件禁用时相关接口 404、前端入口隐藏；单页/整站采集按当前用户的 `collect.page` / `collect.site` 权益放行（无权益 403），基础值即两个子开关，等级/会员可为特定用户放开。整站采集单次页数上限为 `collect.site_max_pages` 权益（基础值 `collect_site_page_limit`，默认 200）。`/site` 额外暴露全站开关 `collect_page_enabled`、`collect_site_enabled`（由插件提供），登录用户以 `/auth/me` 的权益为准。

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| POST | `/collect/site/preview` | JSON `{url,render_mode?}`；抓取根页面，按导航/侧边栏推断目录树，并抽取一个样例页正文供内容区确认。返回 `{root_url,tree:[{url,title,depth,parent_url}],sample:{url,ok,title?,markdown?,error?},limit}` | `collect:create` |
| POST | `/collect/site` | JSON `{root_url,title?,render_mode?,content_selector?,pages:[...]}`；创建草稿书 + 采集任务 + 页面清单并投递后台采集，返回 `{job,book}`。单次页数上限为 `collect.site_max_pages` 权益（基础值 `collect_site_page_limit`，默认 200） | `collect:create` |
| GET | `/books/:id/collect/jobs?kind=site\|chapter` | 本书采集历史（需对书有编辑权） | `collect:read` |
| GET | `/collect/jobs/:id` | 采集任务详情 + 页面清单（按 `sort_order`，供按目录结构展示），仅任务所有者 | `collect:read` |
| POST | `/collect/jobs/:id/retry` | 重试该任务全部失败页并重新入队 | `collect:manage` |
| POST | `/collect/pages/:id/retry` | 重试单个失败页 | `collect:manage` |

> 采集在后台队列执行：逐页抓取→转 Markdown→按 `parent_url` 建章节层级；完成后按成功/失败计数置任务状态（succeeded/partial/failed）并通知用户。进行中（pending/running）的整站采集会让书籍列表卡片与详情页显示「采集中」。

> ZIP 验收标准：导出再导入内容无损（含嵌套章节、草稿状态、评论开关、标签、封面与正文图片）。

## 阅读进度（登录用户）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/reading-progress/:bookId` | 当前用户在可见书籍中的最近阅读章节；无进度返回 `null` | `reading-progress:read` |
| PUT | `/reading-progress/:bookId` | 记录/覆盖进度；`doc_id` 必须属于该书且当前可读，slug/title 由服务端真实章节覆盖；同时将该章节标记为已读。可选 `scroll_percent`（0-100，覆盖写，精确续读）与 `read_seconds_delta`（活跃阅读秒数增量，单次上限 3600，累加到 `read_seconds`） | `reading-progress:update` |
| DELETE | `/reading-progress/:bookId` | 重置当前用户在该书的阅读进度与逐章已读记录（不影响笔记/收藏） | `reading-progress:update` |
| POST | `/reading-progress/:bookId/complete` | 将该书全部已发布章节标记为已读、进度置为最后一章；返回 `{read:<章节数>}` | `reading-progress:update` |
| GET | `/books/:id/read-chapters` | 当前用户在该书已读的章节 ID 列表 `{doc_ids:[]}`，用于详情页进度标记 | `user:read` |
| GET | `/users/me/reading?page=&page_size=` | 「我在读」列表：跨书聚合进度，按最近阅读倒序分页；每项含 `book`、`read_count`、`total_chapters`、`percentage`、`last_doc_slug`、`last_doc_title`、`last_read_at`、`read_seconds` | `reading-progress:read` |
| GET | `/users/me/reading-stats` | 阅读数据概览：`reading_books`（在读）、`completed_books`（已读完）、`chapters_read`（累计已读章节）、`streak_days`（连续阅读天数） | `reading-progress:read` |
| GET | `/users/me/reading-activity?days=N` | 打卡日历：近 N 天（7-366，默认 84）`days:[{date,count,minutes,met}]`（`met` 按当前目标类型判定）+ `current_streak/longest_streak/today_count/today_minutes/today_met` 及 `goal{goal_type,daily_chapters,daily_minutes}`。每日阅读分钟来自阅读器上报的 `read_seconds_delta` 按天累计（`reading_daily_times` 表） | `reading-progress:read` |
| GET | `/users/me/reading-goal` | 每日阅读目标 `{goal_type(chapters\|minutes), daily_chapters, daily_minutes}`（无记录默认章节制 1 章 / 15 分钟） | `reading-progress:read` |
| PUT | `/users/me/reading-goal` | 设置每日阅读目标：`{goal_type, daily_chapters(1-100), daily_minutes(1-600)}`，达标指标随 `goal_type` 切换 | `reading-progress:update` |
| GET | `/users/me/author-analytics?days=N` | 作者仪表盘：本人全部书籍的横向对比。`days` 仅支持 7/30/90/180（默认 30）。返回 `total_books/published_books/total_lifetime_views/total_period_views/total_previous_views/total_growth_percent/total_readers` 及 `books[]`，每项含 `id, title, slug, status, is_public, lifetime_views, period_views, previous_views, growth_percent, chapters, registered_readers, completed_readers, completion_rate, updated_at`（仅统计本人拥有的书籍，浏览量来自最多保留 180 天的每日聚合） | `book-analytics:read` |
| GET | `/users/me/reader-retention` | 读者留存：本人全部书籍以读者「首次阅读周」分组的近 12 周队列。返回 `weeks`、`cohorts[]`（每项 `week` 周一日期、`size` 新读者数、`retention[]` 各周偏移仍活跃的去重读者数，偏移 0 恒等于 size）与 `curve[]`（各周偏移的加权平均留存率百分比，无可观测队列为 null） | `book-analytics:read` |

## 阅读标注与私人笔记（登录用户）

所有数据始终按当前用户隔离。管理员也不能读取或修改其他用户的私人笔记。章节后来变为不可见时，章节标注接口统一返回 404，“我的笔记”聚合列表不再返回该资源；用户仍可凭自己的标注 ID 删除私人数据。

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/documents/:id/annotations` | 当前用户在可见章节中的划线、私人笔记和章节书签 | `annotation:read` |
| POST | `/documents/:id/annotations` | 创建 `highlight\|note\|bookmark`；划线/笔记需提交 `quote/prefix/suffix/start_offset/end_offset`，同章书签重复创建时覆盖 | `annotation:create` |
| PUT | `/annotations/:id` | 更新自己的笔记、颜色、位置与 `active\|relocated\|orphaned` 锚点状态 | `annotation:update` |
| DELETE | `/annotations/:id` | 删除自己的私人标注；越权统一返回 404 | `annotation:delete` |
| GET | `/users/me/annotations` | 分页聚合仍有权访问的私人标注；支持 `kind=highlight\|note\|bookmark` | `annotation:read` |
| GET | `/users/me/annotations/export` | 导出全部私人标注为 Markdown 文件下载（按 书→章节→时间 分组，附 `Content-Disposition`） | `annotation:read` |

锚点以正文文本位置和 `quote + prefix + suffix` 文本片段共同保存。客户端优先校验原位置，章节更新后使用上下文重新定位；无法定位时保留 `quote` 原文快照并标记为 `orphaned`。

响应为进度对象 `{ id, user_id, book_id, doc_id, doc_slug, doc_title, updated_at }`，每用户每书一条（upsert）。

## 系统管理（仅管理员）

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/system/version` | 当前版本/commit + 上游最新版本 + 是否可升级 | `system:read` |
| POST | `/system/upgrade` | 在线升级（下载 Release 资产 → 校验替换 → 重启服务） | `system:upgrade` |
| GET | `/admin/users?page=&page_size=&q=&role=&status=&sort=` | 分页查询用户（`q` 匹配用户名/邮箱，`role` admin\|user，`status` active\|inactive，`sort` created_at_desc\|created_at_asc\|last_login_at_desc\|last_login_at_asc，默认 created_at_desc） | `user:manage` |
| PUT | `/admin/users/:id/role` | 变更角色 `{role: admin\|user}`；禁止操作自身，保留至少一位启用管理员 | `user:manage` |
| PUT | `/admin/users/:id/status` | 启停账户 `{is_active}`；禁止停用自身，保留至少一位启用管理员 | `user:manage` |
| DELETE | `/admin/users/:id` | 删除用户；禁止删除自身，拥有书籍者需先清理书籍 | `user:manage` |
| GET | `/admin/books?page=&page_size=&q=&status=&visibility=&sort=` | 分页查询全站书籍（含草稿、进行中、已发布、已完成、归档及私有内容）；`q` 匹配标题/slug/作者，支持状态、公开性与创建/更新/浏览量排序 | `book:read` + 管理员 |
| GET | `/admin/documents?page=&page_size=&q=&book_id=&status=&sort=` | 分页查询全站章节元数据（不返回正文）；`q` 匹配章节标题/slug/书名/作者，支持按书籍、状态及创建/更新/浏览量排序 | `document:read` + 管理员 |
| GET | `/admin/activity` | 控制台首页时间线：`recent_users`（最近 5 位注册）+ `recent_books`（最近 5 本建书，不限可见性，含草稿/私有） | `user:manage` |
| GET | `/admin/stats` | 管理后台完整统计，包含私有与未发布内容 | `stats:read` + 管理员 |
| GET | `/admin/audit-logs?page=&page_size=&actor=&action=&resource_type=&from=&to=` | 分页查询管理员高风险操作；支持操作人、动作、资源类型与日期区间筛选，日期格式为 `YYYY-MM-DD` | `audit:read` |
| GET | `/admin/audit-logs/facets` | 筛选项：已出现过的 `actions[]` 与 `resource_types[]`（前端按 `admin.audit.actions.*` / `admin.audit.resources.*` 本地化） | `audit:read` |
| GET | `/admin/tasks?page=&page_size=&status=&type=` | 分页查询异步任务；状态支持 pending/running/retrying/succeeded/failed，类型包含 `email.send`、`content.import.pdf`、`content.import.zip`、`maintenance.cleanup`、`achievement.recalculate`，加密任务载荷永不返回 | `task:read` |
| GET | `/admin/tasks/types` | 已注册的全部任务类型 `items[]`（含插件任务，前端按 `admin.tasks.types.*` 本地化） | `task:read` |
| POST | `/admin/tasks/:id/retry` | 将最终失败任务清空旧错误和尝试次数后重新排队；重复操作返回 409 | `task:retry` |
| GET | `/admin/reports?page=&page_size=&status=&target_type=&reason=&q=` | 举报队列与处理记录；`q` 匹配目标摘要、举报人用户名或邮箱；举报人身份仅此管理员接口返回；另返回全部可举报类型 `target_types` | `report:read` |
| PUT | `/admin/reports/:id` | 处理待审举报：`{resolution:"reject"\|"takedown",note?}`；下架会将书籍转为私有归档、章节归档、评论隐藏，插件登记的用户内容由登记者隐藏（在事务之外执行，失败时举报回到待处理），并通知举报人 | `report:update` |
| GET | `/admin/plugins` | 列出后台插件（`key/name/description/kind/builtin/installed/version/status/error`）。`kind=runtime` 为运行时依赖插件（下载二进制），`kind=feature` 为特性开关插件；`installed` 表示已安装/已启用 | `plugin:manage` |
| POST | `/admin/plugins/:key/install` | runtime 插件后台异步安装（pdf-export 下载 chrome-headless-shell），轮询 `/admin/plugins` 看状态；feature 插件（如 achievements）为即时**启用** | `plugin:manage` |
| POST | `/admin/plugins/:key/uninstall[?purge=true]` | runtime 插件卸载并清理下载文件；feature 插件为即时**禁用**（保留数据）。`purge=true` 额外 DROP 该插件独占表（如成就 7 张表，不可恢复）。特性插件禁用后其前端页面与后端接口一并停用（成就管理菜单隐藏、`/admin/achievement-*` 与 `/users/me/achievements` 返回 404），其动态权限也随之移除 | `plugin:manage` |

> **插件数据/权限隔离**：feature 插件的独占表**只在首次启用时建表**（不在核心 AutoMigrate），启用时动态注册其权限，`purge` 卸载时 DROP 表。**成就与标签均已完整隔离**（标签已把 `Book.Tags` 从 GORM many2many 改为代码手动加载 `attachBookTags`，`Tag`/`BookTag` 表随标签插件建/删）。升级到新版本后，已启用插件的表/字段在启动时由 `syncPluginState` 幂等自动迁移。
| GET | `/admin/configs` | 列出全部系统配置键值对（key/value/description/reserved/updated_at） | `config:manage` |
| PUT | `/admin/configs` | 新增或更新配置 `{key,value,description}`；key 限字母数字与 `. _ : -`，≤50 字符 | `config:manage` |
| DELETE | `/admin/configs/:key` | 删除配置键；系统关键项（site_name/site_description/version/installation_date）禁止删除 | `config:manage` |
| GET/PUT | `/admin/ai` | AI 服务（大模型）配置：`provider` openai\|anthropic、`base_url`、`api_key`、`model`，向量嵌入 `embed_base_url`、`embed_api_key`、`embed_model`（OpenAI 兼容）。GET 密钥只返回 `api_key_set`/`embed_api_key_set`，另返回 `source`（ai\|translation\|none，未单独配置时沿用翻译服务的 OpenAI/Claude 配置）、`chat_available`、`embed_available`；另有费用估算单价 `price_currency`（三位代码，默认 USD）、`price_input`、`price_output`、`price_embed`（每百万 tokens）、`price_translate`（Google 翻译，每百万字符）。PUT 只保存传入字段，密钥传空串不修改、传 `-` 清除。供插件经 `Core.AIChat/AIEmbed` 使用 | `site:update` |
| POST | `/admin/ai/test` | `{kind: chat\|embed}` 用当前配置发一次最小请求：返回 `{reply, elapsed_ms}` 或 `{dimensions, elapsed_ms}`，失败 502 | `site:update` |
| GET | `/admin/ai/usage?days=7\|30\|90` | AI 用量统计（逐次调用记录聚合）：`total{calls, errors, input_tokens, output_tokens, cost_micros}`、按日 `daily[]`、`by_feature[]`、`by_model[]`、`top_users[]`、全部功能键 `features[]`、`currency`。站点内所有模型调用（经 `Core.AIChat/AIEmbed` 的 AI 服务调用与翻译服务，测试 `TestModelCallsAreMetered` 禁止绕过）都记录调用方（`ai.WithCaller` 标注的用户/功能/关联对象，0 为系统）、模型、tokens（服务未返回时按文本估算并标记 `estimated`）、耗时与按调用时单价估算的费用（`cost_micros` 为货币单位百万分之一）；删除账号时记录保留但解除关联 | `site:update` |
| GET | `/admin/ai/usage/logs?page=&page_size=&feature=&status=ok\|error&user=&trace_id=` | 调用明细 `items[]{log, username}`（`user` 为用户名，`trace_id` 查看一条调用链的全部调用）。AI 调用不设整体超时（只限制建连与 TLS 握手），由调用方取消。插件可经 `Core.AIChatStream` 以流式接口调用（OpenAI 兼容 `stream` + `include_usage`，不支持时自动去掉 `stream_options` 重试；Anthropic SSE 事件流），计量与额度同 `AIChat` | `site:update` |
| GET | `/admin/ai/alerts?page=` | 用量预警记录 `items[]{alert{kind: site_daily_cost\|user_daily_tokens\|trace_tokens, key, user_id, trace_id, feature, value, threshold, currency}, username}`。阈值在 `/admin/ai` 设置（`alert_daily_cost` 货币单位、`alert_user_daily_tokens`、`alert_trace_tokens`，0 为关闭）；每次调用记账后检查，超过时记录并通知全部管理员，同一对象（当天/用户当天/调用链）只报一次。只报警，不限制调用 | `site:update` |
| GET | `/admin/ai/usage/export?days=&feature=&status=&user=&trace_id=` | 以 CSV（UTF-8 BOM）导出最近 `days` 天（不传则不限时间）符合筛选的调用记录：`time, user, trace_id, feature, ref_type, ref_id, kind, provider, model, input_tokens, output_tokens, characters, estimated, cost, currency, duration_ms, status, error`。`/admin/ai` 的 `usage_retention_days`（0 永久，否则 90–3650）控制保留期，超期记录与预警由每日维护任务分批删除 | `site:update` |
| GET/PUT | `/translation` | 翻译服务配置（写作台与多语言内容的「翻译」按钮）：`provider` none\|google\|openai\|claude、`api_key`、`api_base`、`model` | 管理员 |
| POST | `/translate` | 登录用户翻译文本 `{text(≤20000 字), target_lang?, target_label?, source_lang?, ref_type?: document\|book\|resource, ref_id?}` → `{text}`。每次调用写入 AI 用量记录（功能 `translate`）：OpenAI/Claude 经统一 AI 客户端记录 tokens 与原文字数并受 `ai.monthly_tokens` 约束，Google 按字符记录（`kind=translate`，按 `price_translate` 计价）；所有方式受权益 `translate.monthly_chars`（每月翻译字数，默认不限）约束，剩余不足返回 429 | 登录 |
| GET/PUT | `/admin/achievement-settings` | 读取/保存模块总开关、公开主页展示、解锁通知、允许用户隐藏和陈列数量 | `achievement:manage` |
| GET | `/admin/achievement-metrics` | 返回白名单指标目录、单位、聚合方式、支持的时间窗口和过滤条件 | `achievement:manage` |
| GET/POST | `/admin/achievements` | 分页查询或创建成就定义；列表支持 `q/status/category` | `achievement:manage` |
| GET/PUT/DELETE | `/admin/achievements/:id` | 查看、版本化更新、删除草稿或归档已有生命周期的定义 | `achievement:manage` |
| POST | `/admin/achievements/:id/recalculate` | 把指定 active 自动成就加入后台全用户重算队列，返回 202 与 task | `achievement:manage` |
| POST | `/admin/achievement-icons` | multipart `file`，最大 512KB；支持 PNG/JPEG/GIF/WebP 与白名单净化 SVG | `achievement:manage` |
| GET | `/admin/achievement-presets` | 内置预设成就列表（阅读/创作/互动/账号/签到/成长的阶梯成就，含中英文名称与描述、稀有度、奖励经验、指标与目标）及 `installed`（同 key 成就已存在即视为已安装，含已归档） | `achievement:manage` |
| POST | `/admin/achievement-presets/install` | `{keys?: []}` 安装选中的（缺省为全部）未安装预设，返回 `{installed}`；与手工创建同一套校验、中英文翻译与版本快照，安装后为普通成就。插件首次启用且尚无任何成就时自动安装全部预设 | `achievement:manage` |
| GET/POST | `/admin/achievement-grants` | 分页查询授予记录，或按 `{username,achievement_id,reason?,is_public?}` 人工授予 | `achievement:grant` |
| POST | `/admin/achievement-grants/:id/revoke` | 按 `{reason}` 撤销授予，记录保留并写审计 | `achievement:grant` |

审计日志响应项包含 `actor_id/actor_username/action/resource_type/resource_id/resource_label/summary/created_at`。`summary` 只保存脱敏变更摘要；密码、令牌、OAuth Secret、存储密钥与通用配置值不进入审计记录。
举报处理统一写入 `report.resolved` 审计动作，摘要只记录目标类型、目标 ID 与处理结果，不复制举报人身份或举报正文。
任务载荷使用应用密钥经 AES-GCM 加密后写入数据库；管理员接口只返回类型、状态、尝试次数、时间和截断后的错误信息。服务启动时会把中断超过 5 分钟的 running 任务恢复为 retrying，成功或最终失败记录保留 30 天。

---

## 客户端接入建议

1. **首次使用**：`GET /setup/status` 判断 `installed`；未安装时引导到安装流程（Web 端由服务端 307 到 `/install`）。
2. **会话管理**：登录后保存 `token` 与 `user`；启动时 `GET /auth/me` 校验令牌，401 时清除本地会话。
3. **权限驱动 UI**：`GET /auth/permissions` 的结果缓存于内存，用 `permissions.includes("book:create")` 之类判断是否渲染入口。
4. **浏览计数**：进入书籍/阅读页时 `POST /books/:id/view`，无需等待结果。
5. **Markdown 渲染**：`content` 为 Markdown 原文，客户端自行渲染（Web 端用 marked + highlight.js + DOMPurify）；请过滤 HTML 防 XSS。
