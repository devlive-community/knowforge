// Package authz 定义 KnowForge 的权限模型。
//
// 权限标识统一使用 `功能:权限`（resource:action）格式，例如 user:read、book:create、
// system:upgrade。所有认证后的 API 端点必须在路由注册时通过 RequirePermission 声明
// 所需权限；公开只读端点匿名可访问，但同样在 docs/api.md 中登记其语义权限。
package authz

import "sync"

// Permission 权限标识
type Permission string

// 资源:操作 常量。新增资源时先在此定义，再在路由与 docs/api.md 中登记。
const (
	I18nManage Permission = "i18n:manage"
	// 书籍
	BookRead          Permission = "book:read"           // 浏览书籍列表与详情（含公开匿名访问）
	BookCreate        Permission = "book:create"         // 创建书籍
	BookUpdate        Permission = "book:update"         // 更新书籍（仅限本人或管理员）
	BookDelete        Permission = "book:delete"         // 将书籍移入回收站（仅限本人或管理员）
	BookExport        Permission = "book:export"         // 导出书籍为 markdown zip（owner/admin/editor 协作者）
	BookImport        Permission = "book:import"         // 新建导入书籍，或由 owner/admin 向已有书籍重新导入 PDF
	BookAnalyticsRead Permission = "book-analytics:read" // 查看本人书籍的聚合访问分析（管理员可查看全部）

	// 文档（章节）
	DocumentRead   Permission = "document:read"   // 浏览文档树与正文（含公开匿名访问）
	DocumentCreate Permission = "document:create" // 创建文档（仅限本人书籍）
	DocumentUpdate Permission = "document:update" // 更新文档（仅限本人书籍）
	DocumentDelete Permission = "document:delete" // 将文档子树移入回收站（owner/admin/editor）

	// 章节版本历史
	DocumentRevisionRead    Permission = "document-revision:read"    // 查看章节历史（所有者/admin/editor）
	DocumentRevisionRestore Permission = "document-revision:restore" // 恢复章节历史（所有者/admin/editor）

	// 回收站
	TrashRead    Permission = "trash:read"    // 查看自己的书籍与章节回收站
	TrashRestore Permission = "trash:restore" // 恢复本人书籍或可编辑章节
	TrashDelete  Permission = "trash:delete"  // 永久删除（仅资源所有者或管理员）

	// 标签
	TagRead   Permission = "tag:read"   // 浏览标签与按标签检索（含匿名访问）
	TagCreate Permission = "tag:create" // 创建标签（登录用户，书籍打标时自动创建）
	TagDelete Permission = "tag:delete" // 删除标签（仅管理员）
	TagManage Permission = "tag:manage" // 后台标签管理：重命名、图标、列出全部（仅管理员）

	// 书籍关注（由「书籍关注」插件动态注册）
	FollowRead   Permission = "follow:read"   // 查看自己的关注状态与关注列表
	FollowCreate Permission = "follow:create" // 关注书籍
	FollowDelete Permission = "follow:delete" // 取消关注

	// 用户成长等级（由「成长等级」插件动态注册）
	GrowthRead        Permission = "growth:read"        // 查看自己的成长资料与经验流水
	GrowthUpdate      Permission = "growth:update"      // 修改自己的等级展示偏好
	GrowthManage      Permission = "growth:manage"      // 后台等级/设置管理（仅管理员）
	ExperienceAdjust  Permission = "experience:adjust"  // 人工加减经验（仅管理员）

	// 内容采集插件
	CollectRead   Permission = "collect:read"   // 查看采集任务与历史
	CollectCreate Permission = "collect:create" // 发起采集（单页/整站）
	CollectManage Permission = "collect:manage" // 管理采集任务（重试/删除）

	// 搜索
	SearchRead Permission = "search:read" // 全局搜索书籍与章节（含匿名访问，仅公开内容）

	// 第三方登录
	AuthOauth Permission = "auth:oauth" // 管理第三方登录绑定（登录用户）

	// 找回密码
	AuthPasswordReset Permission = "auth:password-reset" // 申请/执行密码重置（匿名语义，端点公开）

	// 站内通知
	NotificationRead   Permission = "notification:read"   // 查看自己的通知（含 SSE 流）
	NotificationUpdate Permission = "notification:update" // 标记通知已读

	// 我的任务
	UserTaskRead Permission = "user-task:read" // 查看自己发起的后台任务（导入、翻译、采集等）及进度

	// 协作与团队
	CollaboratorRead   Permission = "collaborator:read"   // 查看书籍协作者列表
	CollaboratorCreate Permission = "collaborator:create" // 添加/更新协作者（仅书籍所有者）
	CollaboratorUpdate Permission = "collaborator:update" // 接受或拒绝发给自己的协作邀请
	CollaboratorDelete Permission = "collaborator:delete" // 移除协作者（所有者；协作者可自行退出）

	// 点赞/收藏
	ReactionCreate Permission = "reaction:create" // 点赞/收藏书籍
	ReactionDelete Permission = "reaction:delete" // 取消点赞/收藏
	ReactionRead   Permission = "reaction:read"   // 查看自己的点赞/收藏

	// 阅读进度
	ReadingProgressRead   Permission = "reading-progress:read"   // 查看自己的阅读进度
	ReadingProgressUpdate Permission = "reading-progress:update" // 保存自己的阅读进度
	AnnotationRead        Permission = "annotation:read"         // 查看自己的阅读标注与笔记
	AnnotationCreate      Permission = "annotation:create"       // 创建自己的阅读标注与笔记
	AnnotationUpdate      Permission = "annotation:update"       // 更新自己的阅读标注与笔记
	AnnotationDelete      Permission = "annotation:delete"       // 删除自己的阅读标注与笔记

	// 评论
	CommentRead   Permission = "comment:read"   // 浏览章节评论（含匿名访问）
	CommentCreate Permission = "comment:create" // 发表评论（登录用户）
	CommentUpdate Permission = "comment:update" // 编辑自己的评论
	CommentDelete Permission = "comment:delete" // 删除评论（本人或书籍作者/管理员）

	// 内容举报与审核
	ReportCreate Permission = "report:create" // 举报可见的书籍、章节或评论
	ReportRead   Permission = "report:read"   // 管理员查看举报及举报人信息
	ReportUpdate Permission = "report:update" // 管理员驳回或下架被举报内容

	// 用户
	UserRead   Permission = "user:read"   // 查看用户公开主页
	UserUpdate Permission = "user:update" // 更新个人资料与密码
	UserManage Permission = "user:manage" // 管理后台管理用户：列表/角色/启停/删除（仅管理员）

	// 站点
	SiteRead   Permission = "site:read"   // 读取站点公开配置
	SiteUpdate Permission = "site:update" // 更新站点配置（仅管理员）

	// 系统配置（通用 key-value）
	ConfigManage Permission = "config:manage" // 管理任意系统配置键值对（仅管理员）
	AuditRead    Permission = "audit:read"    // 查看管理员高风险操作审计日志（仅管理员）
	TaskRead     Permission = "task:read"     // 查看异步任务状态与失败诊断（仅管理员）
	TaskRetry    Permission = "task:retry"    // 重新排队失败的异步任务（仅管理员）

	// 统计
	StatsRead Permission = "stats:read" // 读取站点统计（含匿名访问）

	// 上传
	UploadCreate Permission = "upload:create" // 上传图片

	// 系统管理
	SystemRead    Permission = "system:read"    // 查看系统版本信息（仅管理员）
	SystemUpgrade Permission = "system:upgrade" // 触发在线升级（仅管理员）

	// 插件
	PluginManage Permission = "plugin:manage" // 管理后台插件安装/卸载（仅管理员）

	// 成就
	AchievementRead   Permission = "achievement:read"   // 查看自己的成就与进度
	AchievementUpdate Permission = "achievement:update" // 修改自己的公开陈列设置
	AchievementManage Permission = "achievement:manage" // 管理成就定义、规则与模块设置（仅管理员）
	AchievementGrant  Permission = "achievement:grant"  // 人工授予或撤销成就（仅管理员）
)

// All 全部权限，admin 角色默认拥有
var All = []Permission{
	I18nManage,
	BookRead, BookCreate, BookUpdate, BookDelete, BookExport, BookImport, BookAnalyticsRead,
	DocumentRead, DocumentCreate, DocumentUpdate, DocumentDelete,
	DocumentRevisionRead, DocumentRevisionRestore,
	TrashRead, TrashRestore, TrashDelete,
	// 标签权限（TagRead/Create/Delete/Manage）由「标签」插件动态注册，不静态列于此。
	SearchRead,
	AuthOauth, AuthPasswordReset,
	NotificationRead, NotificationUpdate,
	UserTaskRead,
	CollaboratorRead, CollaboratorCreate, CollaboratorUpdate, CollaboratorDelete,
	CommentRead, CommentCreate, CommentUpdate, CommentDelete,
	ReportCreate, ReportRead, ReportUpdate,
	ReactionCreate, ReactionDelete, ReactionRead,
	ReadingProgressRead, ReadingProgressUpdate,
	AnnotationRead, AnnotationCreate, AnnotationUpdate, AnnotationDelete,
	UserRead, UserUpdate, UserManage,
	SiteRead, SiteUpdate, ConfigManage, AuditRead, TaskRead, TaskRetry,
	StatsRead,
	UploadCreate,
	SystemRead, SystemUpgrade,
	PluginManage,
	// 注意：成就权限（AchievementRead/Update/Manage/Grant）由「成就」插件在启用时动态注册，
	// 不再静态列于此，禁用插件即随之移除（见 authz.SetPluginPermissions）。
}

// userPermissions 普通用户（user 角色）拥有的权限
var userPermissions = []Permission{
	BookRead, BookCreate, BookUpdate, BookDelete, BookExport, BookImport, BookAnalyticsRead,
	DocumentRead, DocumentCreate, DocumentUpdate, DocumentDelete,
	DocumentRevisionRead, DocumentRevisionRestore,
	TrashRead, TrashRestore, TrashDelete,
	// 标签权限（TagRead/TagCreate）由「标签」插件动态注册
	SearchRead,
	AuthOauth, AuthPasswordReset,
	NotificationRead, NotificationUpdate,
	UserTaskRead,
	CollaboratorRead, CollaboratorCreate, CollaboratorUpdate, CollaboratorDelete,
	CommentRead, CommentCreate, CommentUpdate, CommentDelete,
	ReportCreate,
	ReactionCreate, ReactionDelete, ReactionRead,
	ReadingProgressRead, ReadingProgressUpdate,
	AnnotationRead, AnnotationCreate, AnnotationUpdate, AnnotationDelete,
	UserRead, UserUpdate,
	SiteRead, StatsRead,
	UploadCreate,
	AchievementRead, AchievementUpdate,
}

// rolePermissions 角色 → 权限映射
var rolePermissions = map[string][]Permission{
	"admin": All,
	"user":  userPermissions,
}

// 插件动态权限：由已启用的 feature 插件贡献，插件禁用时随之移除。
// app 在启动与插件启停时调用 SetPluginPermissions 重算。
var (
	pluginPermsMu    sync.RWMutex
	pluginAdminPerms = map[Permission]bool{}
	pluginUserPerms  = map[Permission]bool{}
)

// SetPluginPermissions 用当前启用插件贡献的权限整体替换动态覆盖层。
// admin 传插件的全部权限，user 传其中面向普通用户的子集。
func SetPluginPermissions(admin, user []Permission) {
	pluginPermsMu.Lock()
	defer pluginPermsMu.Unlock()
	pluginAdminPerms = make(map[Permission]bool, len(admin))
	for _, p := range admin {
		pluginAdminPerms[p] = true
	}
	pluginUserPerms = make(map[Permission]bool, len(user))
	for _, p := range user {
		pluginUserPerms[p] = true
	}
}

func hasPluginPerm(role string, perm Permission) bool {
	pluginPermsMu.RLock()
	defer pluginPermsMu.RUnlock()
	if role == "admin" {
		return pluginAdminPerms[perm] || pluginUserPerms[perm]
	}
	if role == "user" {
		return pluginUserPerms[perm]
	}
	return false
}

// ForRole 返回角色的全部权限（含已启用插件贡献的动态权限）
func ForRole(role string) []Permission {
	perms := rolePermissions[role]
	out := make([]Permission, len(perms))
	copy(out, perms)
	pluginPermsMu.RLock()
	defer pluginPermsMu.RUnlock()
	set := map[Permission]bool{}
	for _, p := range out {
		set[p] = true
	}
	dyn := pluginUserPerms
	if role == "admin" {
		// admin 拥有全部插件权限（user 子集 + admin 专属）
		for p := range pluginUserPerms {
			if !set[p] {
				out = append(out, p)
				set[p] = true
			}
		}
		dyn = pluginAdminPerms
	}
	for p := range dyn {
		if !set[p] {
			out = append(out, p)
			set[p] = true
		}
	}
	return out
}

// Has 判断角色是否拥有指定权限（含已启用插件贡献的动态权限）
func Has(role string, perm Permission) bool {
	for _, p := range rolePermissions[role] {
		if p == perm {
			return true
		}
	}
	return hasPluginPerm(role, perm)
}
