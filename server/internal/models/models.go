package models

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

// User 用户
type User struct {
	PreferredLocale string `gorm:"size:64" json:"preferred_locale"`
	ID              uint   `gorm:"primaryKey" json:"id"`
	Username        string `gorm:"size:50;uniqueIndex" json:"username"`
	Email           string `gorm:"size:100;uniqueIndex" json:"email"`
	Password        string `gorm:"size:255" json:"-"`
	Role            string `gorm:"size:20;default:user" json:"role"`
	Avatar          string `gorm:"size:500" json:"avatar"`
	Bio             string `gorm:"size:1000" json:"bio"`
	GithubURL       string `gorm:"size:255;column:github_url" json:"github_url"`
	// 扩展资料
	Nickname string `gorm:"size:50" json:"nickname"`  // 昵称/展示名
	Website  string `gorm:"size:255" json:"website"`  // 个人网站
	Location string `gorm:"size:100" json:"location"` // 所在地
	Company  string `gorm:"size:100" json:"company"`  // 公司/组织
	IsActive bool   `gorm:"default:true" json:"is_active"`
	// NameDisplay 名字显示方式：空或 nickname 为优先显示昵称（未设置昵称时显示用户名），username 为始终显示用户名
	NameDisplay string `gorm:"size:10;default:''" json:"name_display"`
	// DisplayName 非持久化：按名字显示方式得出的展示名，查询后自动回填（见 AfterFind）
	DisplayName string `gorm:"-" json:"display_name"`
	// TokenBookID 非持久化：以限定书籍的访问令牌访问时只能操作这本书（见 app/access_tokens.go）
	TokenBookID uint `gorm:"-" json:"-"`
	// DeletionRequestedAt 用户自助注销请求时间；非空表示进入冷静期，到期后由维护任务自动删除
	DeletionRequestedAt *time.Time `gorm:"index" json:"deletion_requested_at"`
	// EmailVerified 邮箱是否已激活；开启「注册后必须激活邮箱」时，未激活用户只读
	EmailVerified bool `gorm:"default:false" json:"email_verified"`
	// InviteCode 用户专属邀请码（referral），一经设置不再变化；应用层保证唯一
	InviteCode string `gorm:"size:20;index" json:"invite_code"`
	// InviteCodeEnabled 邀请码是否启用；关闭只是停用，不清除 InviteCode（再开启仍是同一个）
	InviteCodeEnabled bool `gorm:"default:false" json:"invite_code_enabled"`
	// InvitedBy 邀请人用户 ID（0 表示无）
	InvitedBy uint `gorm:"index;default:0" json:"invited_by,omitempty"`
	// TwoFactorEnabled 是否开启二次认证（TOTP）
	TwoFactorEnabled bool `gorm:"default:false" json:"two_factor_enabled"`
	// TwoFactorSecret TOTP 密钥（base32）；不随 JSON 返回
	TwoFactorSecret string `gorm:"size:64" json:"-"`
	// TwoFactorOps 需要二次认证的操作键，逗号分隔：login,credentials,delete,unbind_export
	TwoFactorOps string     `gorm:"size:100" json:"two_factor_ops,omitempty"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Books        []Book     `gorm:"foreignKey:UserID" json:"books,omitempty"`
	// Entitlements 非持久化：/auth/me 回填的当前权益生效值（权益键 → 值），供前端联动入口
	Entitlements    map[string]int64     `gorm:"-" json:"entitlements,omitempty"`
	Authentications []UserAuthentication `gorm:"foreignKey:UserID" json:"authentications,omitempty"`
}

// 名字显示方式。
const (
	NameDisplayNickname = "nickname"
	NameDisplayUsername = "username"
)

// PublicName 展示名：选择显示用户名、或没有昵称时为用户名，否则为昵称。
func (u *User) PublicName() string {
	if u == nil {
		return ""
	}
	if u.NameDisplay == NameDisplayUsername || strings.TrimSpace(u.Nickname) == "" {
		return u.Username
	}
	return u.Nickname
}

// TokenAllowsBook 以限定书籍的访问令牌访问时，是否允许操作该书（未使用令牌或令牌不限书籍时恒为 true）。
func (u *User) TokenAllowsBook(bookID uint) bool {
	return u == nil || u.TokenBookID == 0 || u.TokenBookID == bookID
}

// AfterFind 查询后回填展示名（只选部分列时未选 name_display 按默认的「优先昵称」处理）。
func (u *User) AfterFind(*gorm.DB) error {
	u.DisplayName = u.PublicName()
	return nil
}

// UserAuthentication 第三方登录绑定
type UserAuthentication struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	UserID           uint       `gorm:"index;not null" json:"user_id"`
	Provider         string     `gorm:"size:20;not null;uniqueIndex:uk_provider" json:"provider"`
	ProviderID       string     `gorm:"size:255;not null;uniqueIndex:uk_provider" json:"provider_id"`
	ProviderUsername string     `gorm:"size:100" json:"provider_username"`
	ProviderEmail    string     `gorm:"size:255" json:"provider_email"`
	AccessToken      string     `json:"-"`
	RefreshToken     string     `json:"-"`
	TokenExpiresAt   *time.Time `json:"token_expires_at"`
	IsPrimary        bool       `json:"is_primary"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// SiteConfig 站点键值配置
type SiteConfig struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ConfigKey   string    `gorm:"size:50;uniqueIndex" json:"config_key"`
	ConfigValue string    `json:"config_value"`
	Description string    `gorm:"size:1000" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AuditLog 管理员高风险操作审计记录。Summary 仅保存脱敏后的 JSON 变更摘要。
type AuditLog struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ActorID       uint      `gorm:"index;not null" json:"actor_id"`
	ActorUsername string    `gorm:"size:50;index;not null" json:"actor_username"`
	Action        string    `gorm:"size:80;index;not null" json:"action"`
	ResourceType  string    `gorm:"size:50;index;not null" json:"resource_type"`
	ResourceID    string    `gorm:"size:100;index;not null" json:"resource_id"`
	ResourceLabel string    `gorm:"size:255" json:"resource_label"`
	Summary       string    `gorm:"not null" json:"-"`
	CreatedAt     time.Time `gorm:"index;not null" json:"created_at"`
}

// Notification 站内通知（M13）
type Notification struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	Type      string     `gorm:"size:30;index" json:"type"` // comment | reaction | system | collaboration
	Title     string     `gorm:"size:255;not null" json:"title"`
	Payload   string     `json:"payload"` // JSON 字符串，如 {"link":"/book/detail/x"}
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// BookCollaborator 书籍协作者（M14；书籍所有者为 book.user_id，不在此表）
type BookCollaborator struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	BookID      uint       `gorm:"not null;uniqueIndex:uk_book_user" json:"book_id"`
	UserID      uint       `gorm:"not null;uniqueIndex:uk_book_user" json:"user_id"`
	Role        string     `gorm:"size:20;default:editor" json:"role"`           // editor | viewer
	Status      string     `gorm:"size:20;default:accepted;index" json:"status"` // pending | accepted | rejected
	InvitedBy   uint       `gorm:"index;default:0" json:"invited_by"`
	TeamID      uint       `gorm:"index;default:0" json:"team_id"` // 非 0：权限来自书籍所属团队（团队插件维护），不能在协作者中直接修改或移除
	RespondedAt *time.Time `json:"responded_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	User        *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Book        *Book      `gorm:"foreignKey:BookID" json:"book,omitempty"`
}

// PasswordResetToken 找回密码一次性令牌（M15；只存哈希，明文仅出现在邮件链接里）
type PasswordResetToken struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TokenHash string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"index" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// CaptchaChallenge 验证码挑战：存数据库以支持多实例部署；只存答案哈希，一次性、有有效期。
type CaptchaChallenge struct {
	ID         string    `gorm:"primaryKey;size:64" json:"id"`
	AnswerHash string    `gorm:"size:64;not null" json:"-"`
	ExpiresAt  time.Time `gorm:"index" json:"-"`
	CreatedAt  time.Time `json:"-"`
}

// OAuthState 第三方登录 CSRF state：存数据库以支持多实例部署（回调可能落到另一实例）；
// 只存 state 的哈希（敏感值不落库明文），一次性、有有效期。
type OAuthState struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	StateHash string    `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Origin    string    `gorm:"size:512" json:"-"`
	UserID    uint      `gorm:"default:0" json:"-"` // 非 0 表示「为该已登录用户绑定」模式（否则为登录/注册）
	ExpiresAt time.Time `gorm:"index" json:"-"`
	CreatedAt time.Time `json:"-"`
}

// LoginChallenge 登录二次认证的中间态：密码+验证码通过但还需 TOTP 时下发，
// 只存 token 哈希，一次性、有有效期。存数据库以支持多实例（第二步可能落到另一实例）。
type LoginChallenge struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	TokenHash string    `gorm:"size:64;uniqueIndex;not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"-"`
	ExpiresAt time.Time `gorm:"index" json:"-"`
	CreatedAt time.Time `json:"-"`
}

// RateLimitCounter 限流计数：存数据库以支持多实例部署（跨实例共享固定窗口计数）。
// RateKey 已是脱敏哈希（policy 名 + 主体的 sha256），不含明文；ResetAt 为窗口结束时间。
type RateLimitCounter struct {
	RateKey string    `gorm:"primaryKey;column:rate_key;size:128" json:"-"`
	Count   int       `gorm:"not null;default:0" json:"-"`
	ResetAt time.Time `gorm:"index" json:"-"`
}

// TwoFactorStepUp 二次认证 step-up 授权窗口：存数据库以支持多实例部署，每用户一条。
type TwoFactorStepUp struct {
	UserID    uint      `gorm:"primaryKey" json:"user_id"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
}

// UserNotificationPref 用户邮件通知偏好（每用户一条，缺省全部开启）。
type UserNotificationPref struct {
	UserID        uint `gorm:"primaryKey" json:"user_id"`
	Comment       bool `gorm:"default:true" json:"comment"`
	Reaction      bool `gorm:"default:true" json:"reaction"`
	Collaboration bool `gorm:"default:true" json:"collaboration"`
	Moderation    bool `gorm:"default:true" json:"moderation"`
	System        bool `gorm:"default:true" json:"system"`
	Achievement   bool `gorm:"default:true" json:"achievement"`
	BookUpdate    bool `gorm:"default:true" json:"book_update"` // 关注书籍更新通知
	Growth        bool `gorm:"default:true" json:"growth"`      // 成长升级通知
	// DigestMode 邮件发送方式：instant 每条即时发送（默认）| daily 每日摘要 | weekly 每周摘要 | off 不发送邮件
	DigestMode   string     `gorm:"size:16;default:instant" json:"digest_mode"`
	LastDigestAt *time.Time `json:"-"` // 上次发送摘要（或检查后无内容）的时间
}

// LoginLockout 登录失败锁定计数（每账户一条，多实例共享）。
type LoginLockout struct {
	Username    string    `gorm:"primaryKey;size:100" json:"username"`
	Fails       int       `json:"fails"`
	WindowStart time.Time `json:"window_start"`
	LockedUntil time.Time `json:"locked_until"`
}

// TwoFactorBackupCode 二次认证备用码：一次性，数据库只存哈希，供丢失验证器时恢复。
type TwoFactorBackupCode struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	CodeHash  string     `gorm:"size:64;index;not null" json:"-"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// EmailVerificationToken 邮箱激活令牌：一次性、有有效期，数据库只存 SHA-256 哈希。
type EmailVerificationToken struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TokenHash string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"index" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// WriterComment 写作批注：协作者在写作台对章节正文的批注与讨论，只有能编辑该书的人可见（不出现在阅读页）。
// 顶级批注锚定一段原文（Quote 及前后文与大致位置，正文修改后按此重新定位）；回复的 ParentID 指向顶级批注。
type WriterComment struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	BookID      uint       `gorm:"index;not null" json:"book_id"`
	DocumentID  uint       `gorm:"index;not null" json:"document_id"`
	UserID      uint       `gorm:"index;not null" json:"user_id"`
	ParentID    *uint      `gorm:"index" json:"parent_id"`
	Quote       string     `gorm:"type:text" json:"quote"`
	Prefix      string     `gorm:"size:255" json:"prefix"`
	Suffix      string     `gorm:"size:255" json:"suffix"`
	QuoteOffset int        `gorm:"default:0" json:"quote_offset"`
	Content     string     `gorm:"type:text;not null" json:"content"`
	Resolved    bool       `gorm:"index;default:false" json:"resolved"`
	ResolvedBy  uint       `gorm:"default:0" json:"resolved_by"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// WriterSuggestion 修改建议：「建议者」协作者对章节提交的修改（保存其依据的原文与建议后的标题、正文），
// 由能编辑的人逐处审阅、部分或全部采纳（合并到当前版本）或拒绝。
type WriterSuggestion struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	BookID      uint       `gorm:"index;not null" json:"book_id"`
	DocumentID  uint       `gorm:"index;not null" json:"document_id"`
	UserID      uint       `gorm:"index;not null" json:"user_id"`
	BaseTitle   string     `gorm:"size:255" json:"base_title"`
	BaseContent string     `gorm:"type:text" json:"base_content"`
	Title       string     `gorm:"size:255" json:"title"`
	Content     string     `gorm:"type:text" json:"content"`
	Note        string     `gorm:"size:1024" json:"note"`
	Status      string     `gorm:"size:16;index;not null;default:pending" json:"status"` // pending | accepted | partial | rejected
	DecidedBy   uint       `gorm:"default:0" json:"decided_by"`
	DecidedAt   *time.Time `json:"decided_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// WriterChapterTask 章节分工：负责人、写作阶段与截止日期（每章至多一条，由能编辑的人在写作台设置）。
type WriterChapterTask struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	BookID     uint       `gorm:"index;not null" json:"book_id"`
	DocumentID uint       `gorm:"uniqueIndex;not null" json:"document_id"`
	AssigneeID uint       `gorm:"index;default:0" json:"assignee_id"`
	Stage      string     `gorm:"size:16;index;not null;default:todo" json:"stage"` // todo | writing | review | done
	DueAt      *time.Time `json:"due_at"`
	UpdatedBy  uint       `gorm:"default:0" json:"updated_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// WriterActivity 协作动态：写作相关的操作记录（保存、新建、删除章节，批注、修改建议、分工变化等），
// 同一人连续保存同一章时合并为一条（Detail 中累加改动行数）。保留 180 天。
type WriterActivity struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	BookID     uint      `gorm:"index:idx_writer_activity_book,priority:1;not null" json:"book_id"`
	DocumentID uint      `gorm:"index;default:0" json:"document_id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	Kind       string    `gorm:"size:32;not null" json:"kind"`
	Detail     string    `gorm:"type:text" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `gorm:"index:idx_writer_activity_book,priority:2" json:"updated_at"`
}

// WriterPresence 写作台在线状态：每个打开的写作台事件流一条（连接断开时删除，SeenAt 由服务端心跳刷新，过期即视为离开）。
type WriterPresence struct {
	ConnID    string    `gorm:"primaryKey;size:32" json:"conn_id"`
	BookID    uint      `gorm:"index;not null" json:"book_id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	DocID     uint      `json:"doc_id"`
	Dirty     bool      `json:"dirty"` // 当前章节有未保存的修改
	SeenAt    time.Time `gorm:"index" json:"seen_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (WriterPresence) TableName() string { return "writer_presences" }

// SystemBackup 站点备份记录：备份文件为 zip（manifest.json + 各表数据 + 可选的本地上传文件），保存在数据目录 backups/ 下。
// 备份含站点密钥、密码哈希与各类接口密钥，不上传到用于公开访问的对象存储。
type SystemBackup struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Name         string     `gorm:"size:200;not null;index" json:"name"`
	Kind         string     `gorm:"size:16;not null;index" json:"kind"`   // manual | scheduled
	Status       string     `gorm:"size:16;not null;index" json:"status"` // running | done | failed
	IncludeFiles bool       `json:"include_files"`
	Size         int64      `json:"size"`
	Tables       int        `json:"tables"`
	Rows         int64      `json:"rows"`
	Files        int        `json:"files"`
	Stage        string     `gorm:"size:16" json:"stage"` // database | files
	Done         int        `json:"done"`
	Total        int        `json:"total"`
	Error        string     `gorm:"type:text" json:"error"`
	AppVersion   string     `gorm:"size:32" json:"app_version"`
	CreatedBy    uint       `gorm:"index" json:"created_by"`
	CreatedAt    time.Time  `gorm:"index" json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

func (SystemBackup) TableName() string { return "system_backups" }

// BackgroundJob 持久化异步任务。Payload 以应用密钥加密保存，不通过 API 返回。
type BackgroundJob struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	OwnerID     uint       `gorm:"index;not null;default:0" json:"owner_id"`
	Type        string     `gorm:"size:80;index;not null" json:"type"`
	Payload     string     `gorm:"not null" json:"-"`
	Result      string     `json:"-"`
	Status      string     `gorm:"size:20;index;not null;default:pending" json:"status"` // pending | running | retrying | succeeded | failed
	Attempts    int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts int        `gorm:"not null;default:5" json:"max_attempts"`
	AvailableAt time.Time  `gorm:"index;not null" json:"available_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	LockedAt    *time.Time `gorm:"index" json:"-"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AchievementAsset 成就专用图标资源。SVG 只保存经过服务端安全校验后的内容。
type AchievementAsset struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Kind       string    `gorm:"size:20;not null" json:"kind"` // image | svg
	URL        string    `gorm:"size:500;not null" json:"url"`
	MimeType   string    `gorm:"size:100;not null" json:"mime_type"`
	Width      int       `gorm:"default:0" json:"width"`
	Height     int       `gorm:"default:0" json:"height"`
	SHA256     string    `gorm:"size:64;uniqueIndex;not null" json:"sha256"`
	UploadedBy uint      `gorm:"index;not null" json:"uploaded_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// AchievementDefinition 成就定义；规则单独存表，避免把可查询配置塞进站点 JSON。
type AchievementDefinition struct {
	Translations       json.RawMessage   `gorm:"-" json:"translations,omitempty"`
	ResolvedLocale     string            `gorm:"-" json:"resolved_locale,omitempty"`
	ID                 uint              `gorm:"primaryKey" json:"id"`
	Key                string            `gorm:"column:achievement_key;size:80;uniqueIndex;not null" json:"key"`
	Name               string            `gorm:"size:120;not null" json:"name"`
	NameEn             string            `gorm:"size:120" json:"name_en"`
	Description        string            `gorm:"size:500" json:"description"`
	DescriptionEn      string            `gorm:"size:500" json:"description_en"`
	LockedHint         string            `gorm:"size:255" json:"locked_hint"`
	LockedHintEn       string            `gorm:"size:255" json:"locked_hint_en"`
	Category           string            `gorm:"size:30;index;not null" json:"category"`
	Status             string            `gorm:"size:20;index;default:draft" json:"status"` // draft | active | paused | archived
	Rarity             string            `gorm:"size:20;default:common" json:"rarity"`
	IconType           string            `gorm:"size:20;default:fa" json:"icon_type"` // fa | image | svg
	IconValue          string            `gorm:"size:500" json:"icon_value"`
	AssetID            *uint             `gorm:"index" json:"asset_id"`
	Asset              *AchievementAsset `gorm:"foreignKey:AssetID" json:"asset,omitempty"`
	SeriesKey          string            `gorm:"size:80;index" json:"series_key"`
	Tier               int               `gorm:"default:1" json:"tier"`
	RewardXP           int               `gorm:"default:0" json:"reward_xp"` // 解锁奖励成长经验（成长插件启用时生效）
	SupersedesPrevious bool              `gorm:"default:false" json:"supersedes_previous"`
	RuleLogic          string            `gorm:"size:10;default:all" json:"rule_logic"`    // all | any
	GrantMode          string            `gorm:"size:20;default:auto" json:"grant_mode"`   // auto | manual
	Visibility         string            `gorm:"size:20;default:public" json:"visibility"` // public | private | hidden
	ProgressMode       string            `gorm:"size:20;default:aggregate" json:"progress_mode"`
	ActiveFrom         *time.Time        `gorm:"index" json:"active_from"`
	ActiveUntil        *time.Time        `gorm:"index" json:"active_until"`
	Version            int               `gorm:"default:1" json:"version"`
	SortOrder          int               `gorm:"default:0;index" json:"sort_order"`
	CreatedBy          uint              `gorm:"index;not null" json:"created_by"`
	UpdatedBy          uint              `gorm:"index;not null" json:"updated_by"`
	Rules              []AchievementRule `gorm:"foreignKey:AchievementID" json:"rules"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// AchievementRule 是后端白名单指标上的单条条件，不允许保存 SQL 或脚本。
type AchievementRule struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AchievementID uint      `gorm:"index;not null" json:"achievement_id"`
	MetricKey     string    `gorm:"size:80;index;not null" json:"metric_key"`
	Operator      string    `gorm:"size:20;default:gte" json:"operator"` // gte | eq | between
	TargetValue   int64     `gorm:"not null" json:"target_value"`
	TargetMax     int64     `gorm:"default:0" json:"target_max"`
	WindowType    string    `gorm:"size:20;default:lifetime" json:"window_type"`
	WindowValue   int       `gorm:"default:0" json:"window_value"`
	DistinctBy    string    `gorm:"size:30" json:"distinct_by"`
	Filters       string    `json:"filters"`
	SortOrder     int       `gorm:"default:0" json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AchievementDefinitionVersion 保存每次发布后可审计的完整定义与规则快照。
type AchievementDefinitionVersion struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AchievementID uint      `gorm:"uniqueIndex:uk_achievement_version;index;not null" json:"achievement_id"`
	Version       int       `gorm:"uniqueIndex:uk_achievement_version;not null" json:"version"`
	Snapshot      string    `gorm:"not null" json:"snapshot"`
	CreatedBy     uint      `gorm:"index;not null" json:"created_by"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

// UserAchievementProgress 保存用户对某个成就的最近一次可解释评估结果。
type UserAchievementProgress struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	UserID            uint      `gorm:"uniqueIndex:uk_user_achievement_progress;not null" json:"user_id"`
	AchievementID     uint      `gorm:"uniqueIndex:uk_user_achievement_progress;index;not null" json:"achievement_id"`
	DefinitionVersion int       `gorm:"not null" json:"definition_version"`
	CurrentValue      int64     `gorm:"default:0" json:"current_value"`
	Percent           int       `gorm:"default:0" json:"percent"`
	RuleValues        string    `json:"rule_values"`
	Status            string    `gorm:"size:20;default:pending;index" json:"status"` // pending | unlocked
	LastEvaluatedAt   time.Time `gorm:"index" json:"last_evaluated_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// UserAchievement 是授予事实；撤销保留记录和原因，不物理删除。
type UserAchievement struct {
	ID                uint                   `gorm:"primaryKey" json:"id"`
	UserID            uint                   `gorm:"uniqueIndex:uk_user_achievement;index;not null" json:"user_id"`
	AchievementID     uint                   `gorm:"uniqueIndex:uk_user_achievement;index;not null" json:"achievement_id"`
	Achievement       *AchievementDefinition `gorm:"foreignKey:AchievementID" json:"achievement,omitempty"`
	DefinitionVersion int                    `gorm:"not null" json:"definition_version"`
	Source            string                 `gorm:"size:20;default:auto" json:"source"` // auto | manual
	GrantorID         uint                   `gorm:"index;default:0" json:"grantor_id"`
	Reason            string                 `gorm:"size:500" json:"reason"`
	MetricsSnapshot   string                 `json:"metrics_snapshot"`
	IsPublic          bool                   `gorm:"index" json:"is_public"`
	ShowcaseOrder     int                    `gorm:"default:0;index" json:"showcase_order"`
	UnlockedAt        time.Time              `gorm:"index;not null" json:"unlocked_at"`
	NotifiedAt        *time.Time             `json:"notified_at"`
	RevokedAt         *time.Time             `gorm:"index" json:"revoked_at"`
	RevokedBy         uint                   `gorm:"index;default:0" json:"revoked_by"`
	RevokeReason      string                 `gorm:"size:500" json:"revoke_reason"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// AchievementEvent 是成就评估的持久化触发记录；唯一键保证业务重试不会重复排队。
type AchievementEvent struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	DedupeKey   string     `gorm:"size:160;uniqueIndex;not null" json:"-"`
	UserID      uint       `gorm:"index;not null" json:"user_id"`
	Type        string     `gorm:"size:80;index;not null" json:"type"`
	SourceType  string     `gorm:"size:40" json:"source_type"`
	SourceID    string     `gorm:"size:100" json:"source_id"`
	EnqueuedAt  *time.Time `gorm:"index" json:"enqueued_at"`
	ProcessedAt *time.Time `gorm:"index" json:"processed_at"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
}

// Book 书籍
type Book struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Title       string `gorm:"size:255;not null" json:"title"`
	Description string `json:"description"`
	CoverImage  string `gorm:"size:500" json:"cover_image"`
	Slug        string `gorm:"size:255;uniqueIndex;not null" json:"slug"`
	// SlugEditable 是否还允许修改访问路径（slug）。复制出的书籍为 true，修改一次后自动置为 false（只能改一次）。
	SlugEditable bool   `gorm:"default:false" json:"slug_editable"`
	UserID       uint   `gorm:"index;not null" json:"user_id"`
	Status       string `gorm:"size:20;default:draft;index" json:"status"` // draft | in_progress | published | completed | archived
	IsPublic     bool   `gorm:"default:false;index" json:"is_public"`
	// LoginRequired 公开书籍是否仅限登录用户阅读/发现：开启后未登录游客既看不到也读不到，登录用户不受限
	LoginRequired bool   `gorm:"default:false;index" json:"login_required"`
	ViewCount     int    `gorm:"default:0" json:"view_count"`
	OrderCol      string `gorm:"size:50;default:created_at" json:"order_col"`
	OrderDir      string `gorm:"size:10;default:desc" json:"order_dir"`
	ChapterPrefix string `gorm:"size:20;default:''" json:"chapter_prefix"`
	// DefaultChapterStatus 新建「第一级」章节的默认发布状态（draft|published|archived），未显式指定状态时生效
	DefaultChapterStatus string `gorm:"size:16;default:'draft'" json:"default_chapter_status"`
	// ChildStatusFollowParent 新建子章节时默认发布状态跟随父章节（写作台创建时生效）
	ChildStatusFollowParent bool `gorm:"default:false" json:"child_status_follow_parent"`
	// Language 书籍语言标签（如「中文」/「English」），配合 TransGroup 组成多语言互译组
	Language string `gorm:"size:32;default:''" json:"language"`
	// TransGroup 翻译分组标识：填相同非空标识的书籍互为翻译，阅读页可切换语言
	TransGroup string `gorm:"size:64;default:'';index" json:"trans_group"`
	// Version 版本标签（如「v1」/「第一版」），配合 VersionGroup 组成多版本书组
	Version string `gorm:"size:32;default:''" json:"version"`
	// VersionGroup 版本分组标识：填相同非空标识的书籍互为不同版本，阅读页可切换版本
	VersionGroup string `gorm:"size:64;default:'';index" json:"version_group"`
	// VersionIsLatest 标记本书为版本组内的「最新版」，阅读页/详情页版本选择器旁展示「最新版」标记
	VersionIsLatest  bool   `gorm:"default:false" json:"version_is_latest"`
	WatermarkEnabled bool   `gorm:"default:false" json:"watermark_enabled"`
	WatermarkText    string `gorm:"size:255;default:''" json:"watermark_text"`
	// ExportEnabled 作者是否允许他人导出本书（公开书籍生效；作者/协作者不受限）
	ExportEnabled bool `gorm:"default:true" json:"export_enabled"`
	// GuestExportEnabled 作者是否允许未登录游客导出本书（需先开启 ExportEnabled；关闭后仅登录读者可导出）
	GuestExportEnabled bool `gorm:"default:true" json:"guest_export_enabled"`
	// ExportStyleShared 作者是否共享自己的导出样式：开启后他人导出本书可选用作者样式，否则只能用自己的
	ExportStyleShared bool `gorm:"default:false" json:"export_style_shared"`
	// ExportFormats 逗号分隔的允许导出格式（pdf,markdown）；空表示全部格式可用
	ExportFormats string `gorm:"size:100;default:''" json:"export_formats"`
	// ExtraInfo 「更多信息」附加属性（GitHub、原始文档地址、许可证等），书籍详情页展示
	ExtraInfo BookInfo `gorm:"type:text" json:"extra_info"`
	User      *User    `gorm:"foreignKey:UserID" json:"user,omitempty"`
	// Tags 由代码手动加载（attachBookTags），不走 GORM many2many——避免核心 Book 硬依赖标签插件表。
	Tags []Tag `gorm:"-" json:"tags,omitempty"`
	// Crawling 该书是否有进行中的整站采集任务（由 attachCrawlingFlags 按需填充，供列表/详情显示「采集中」）。
	Crawling   bool           `gorm:"-" json:"crawling,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy  uint           `gorm:"index;default:0" json:"-"`
	TrashGroup string         `gorm:"size:64;index" json:"-"`
	// ChapterCount 非持久化：列表接口按需回填的章节（文档）数量
	ChapterCount int `gorm:"-" json:"chapter_count"`
	// CollaboratorRole 非持久化：协作书籍列表按需回填当前用户的角色。
	CollaboratorRole string `gorm:"-" json:"collaborator_role,omitempty"`
	// VersionCount 非持久化：「版本聚合」列表中本书所在版本组、在当前筛选下可见的版本数（>1 才返回）。
	VersionCount int `gorm:"-" json:"version_count,omitempty"`
	// LatestVersion 非持久化：本书所在版本组中被标记为「最新版」的书籍的版本号（列表卡片展示「最新版本 xxx」）。
	LatestVersion string `gorm:"-" json:"latest_version,omitempty"`
	// PublishHeld 非持久化：本次保存中「公开」被发布守卫（如内容审核）拦截时的说明，书籍保持私有
	PublishHeld string `gorm:"-" json:"publish_held,omitempty"`
}

// Tag 标签
type Tag struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Slug      string    `gorm:"size:50;uniqueIndex;not null" json:"slug"`
	IconType  string    `gorm:"size:10;default:''" json:"icon_type"` // "" | fa | image | svg
	IconValue string    `gorm:"size:500" json:"icon_value"`          // fa 类名，或上传后的媒体地址
	BookCount int64     `gorm:"->" json:"book_count"`                // 只读聚合列：公开书籍使用计数
	CreatedAt time.Time `json:"created_at"`
}

// Comment 章节评论：支持两级（parent_id 为空是顶层评论）
type Comment struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	DocumentID uint      `gorm:"index;not null" json:"document_id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	User       *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	ParentID   *uint     `gorm:"index" json:"parent_id"`
	Parent     *Comment  `gorm:"foreignKey:ParentID" json:"-"`
	Content    string    `gorm:"not null" json:"content"`
	Status     string    `gorm:"size:20;default:published;index" json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BookReview 书籍评价：每位用户对每本书一条（评分 1-5 + 可选文字），用于详情页评分与评论。
type BookReview struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BookID    uint      `gorm:"index;not null;uniqueIndex:uk_book_user_review" json:"book_id"`
	UserID    uint      `gorm:"index;not null;uniqueIndex:uk_book_user_review" json:"user_id"`
	User      *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Rating    int       `gorm:"not null" json:"rating"` // 1-5
	Content   string    `json:"content"`
	Status    string    `gorm:"size:20;default:published;index" json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ContentReport 用户对书籍、章节或评论提交的内容举报及管理员处理记录。
type ContentReport struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	ReporterID     uint       `gorm:"index;not null" json:"reporter_id"`
	TargetType     string     `gorm:"size:20;index;not null" json:"target_type"` // book | document | comment
	TargetID       uint       `gorm:"index;not null" json:"target_id"`
	TargetLabel    string     `gorm:"size:255;not null" json:"target_label"`
	Reason         string     `gorm:"size:30;index;not null" json:"reason"`
	Description    string     `json:"description"`
	Status         string     `gorm:"size:20;index;default:pending" json:"status"` // pending | rejected | resolved
	Resolution     string     `gorm:"size:20" json:"resolution"`                   // reject | takedown
	ResolutionNote string     `json:"resolution_note"`
	HandlerID      uint       `gorm:"index;default:0" json:"handler_id"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Reaction 点赞/收藏：每用户每书一条（like 或 favorite）
type Reaction struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_user_book_type;not null" json:"user_id"`
	BookID    uint      `gorm:"uniqueIndex:uk_user_book_type;index;not null" json:"book_id"`
	Type      string    `gorm:"uniqueIndex:uk_user_book_type;size:20;not null" json:"type"` // like | favorite
	CreatedAt time.Time `json:"created_at"`
	User      *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// ---- 用户成长等级（「成长等级」插件，默认关闭；建表由插件负责）----

// LevelDefinition 等级定义：阈值严格递增，等级 1 的 min_xp=0。名称/说明 MVP 用固定字段。
type LevelDefinition struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Level       int    `gorm:"uniqueIndex;not null" json:"level"` // 等级编号（1 起，唯一）
	Key         string `gorm:"size:50" json:"key"`
	Name        string `gorm:"size:120" json:"name"`
	Description string `gorm:"size:500" json:"description"`
	IconType    string `gorm:"size:10;default:'fa'" json:"icon_type"` // fa | image | svg
	IconValue   string `gorm:"size:500;default:'fa-star'" json:"icon_value"`
	Color       string `gorm:"size:20;default:''" json:"color"`
	MinXP       int    `gorm:"not null;default:0" json:"min_xp"`
	SortOrder   int    `gorm:"default:0" json:"sort_order"`
	Status      string `gorm:"size:20;default:'active'" json:"status"` // active | archived
	// Entitlements 达到该等级后获得的权益（累计：当前等级及以下各等级的配置依次覆盖）；未设置的键不改变
	Entitlements EntitlementMap `gorm:"type:text" json:"entitlements"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// UserGrowthProfile 用户成长资料：由 ExperienceEvent 汇总的权威快照（可从流水重建）。
type UserGrowthProfile struct {
	UserID       uint      `gorm:"primaryKey" json:"user_id"`
	LifetimeXP   int64     `gorm:"default:0" json:"lifetime_xp"`
	CurrentLevel int       `gorm:"default:1" json:"current_level"` // 当前等级编号
	HighestLevel int       `gorm:"default:1" json:"highest_level"` // 达到过的最高等级
	Public       bool      `gorm:"default:true" json:"public"`     // 是否公开展示等级
	UpdatedAt    time.Time `json:"updated_at"`
}

// ExperienceRule 经验规则：可配置各事件类型的基础经验与每日上限（管理员在成长后台维护）。
type ExperienceRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	RuleKey   string    `gorm:"size:60;uniqueIndex;not null" json:"rule_key"` // 如 reading.chapter / creation.chapter_published / community.comment
	Label     string    `gorm:"size:120" json:"label"`
	BaseXP    int       `gorm:"default:0" json:"base_xp"`
	DailyCap  int       `gorm:"default:0" json:"daily_cap"` // 0=不限；每人每天该规则经验上限
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	SortOrder int       `gorm:"default:0" json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ExperienceEvent 不可变经验流水：dedupe_key 唯一保证幂等（重试/并发只落一条）。
type ExperienceEvent struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	RuleKey    string    `gorm:"size:60;not null" json:"rule_key"` // 如 achievement.unlocked / reading.chapter / admin.adjust
	SourceType string    `gorm:"size:30" json:"source_type"`
	SourceID   string    `gorm:"size:60" json:"source_id"`
	DedupeKey  string    `gorm:"size:120;uniqueIndex;not null" json:"-"`
	BaseXP     int       `json:"base_xp"`
	FinalXP    int       `json:"final_xp"`
	Reason     string    `gorm:"size:255" json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// UserLevelHistory 升级/降级/人工调整历史。
type UserLevelHistory struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	FromLevel int       `json:"from_level"`
	ToLevel   int       `json:"to_level"`
	Reason    string    `gorm:"size:120" json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// UserCheckin 每日签到（成长插件）：每用户每个自然日（服务器本地时区）一条；Streak 为截至当天的连续签到天数。
type UserCheckin struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_user_checkin_day;not null" json:"user_id"`
	Day       string    `gorm:"size:10;uniqueIndex:uk_user_checkin_day;not null" json:"day"` // YYYY-MM-DD
	Streak    int       `gorm:"not null" json:"streak"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// BookFollow 模型已迁至「书籍关注」插件子包 internal/plugins/bookfollow/models.go
// （插件独占表，随插件启用建表、卸载清除；核心不再引用该类型）。

// ReadingProgress 阅读进度：每个用户在每个书籍中最近读到的章节
type ReadingProgress struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	UserID   uint   `gorm:"uniqueIndex:uk_user_book;not null" json:"user_id"`
	BookID   uint   `gorm:"uniqueIndex:uk_user_book;not null" json:"book_id"`
	DocID    uint   `gorm:"not null" json:"doc_id"`
	DocSlug  string `gorm:"size:255;not null" json:"doc_slug"`
	DocTitle string `gorm:"size:255" json:"doc_title"`
	// ScrollPercent 最近章节的滚动百分比（0-100），用于精确续读定位
	ScrollPercent int `gorm:"default:0" json:"scroll_percent"`
	// ReadSeconds 该书累计阅读秒数（由阅读器活跃计时增量累加）
	ReadSeconds int       `gorm:"default:0" json:"read_seconds"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ReadChapter 记录用户读过的每个章节（用于详情页阅读进度标记），每用户每章一条
type ReadChapter struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_user_doc;not null" json:"user_id"`
	BookID    uint      `gorm:"index;not null" json:"book_id"`
	DocID     uint      `gorm:"uniqueIndex:uk_user_doc;not null" json:"doc_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ReadingAnnotation 用户在章节内创建的私人划线、笔记或章节书签。
// Quote 始终保留创建时的原文快照，章节更新导致锚点失效时也不会丢失。
type ReadingAnnotation struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index;not null" json:"user_id"`
	BookID       uint      `gorm:"index;not null" json:"book_id"`
	DocumentID   uint      `gorm:"index;not null" json:"document_id"`
	Kind         string    `gorm:"size:20;index;not null" json:"kind"` // highlight | note | bookmark
	Color        string    `gorm:"size:20;default:yellow" json:"color"`
	Note         string    `json:"note"`
	Quote        string    `json:"quote"`
	Prefix       string    `gorm:"size:500" json:"prefix"`
	Suffix       string    `gorm:"size:500" json:"suffix"`
	StartOffset  int       `gorm:"default:0" json:"start_offset"`
	EndOffset    int       `gorm:"default:0" json:"end_offset"`
	AnchorStatus string    `gorm:"size:20;default:active" json:"anchor_status"` // active | relocated | orphaned
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// BookAnalyticsDaily 按自然日、章节与来源聚合浏览量。只保存聚合桶，不保存 IP、
// User-Agent 或原始 Referer，兼顾作者分析与读者隐私。
type BookAnalyticsDaily struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	BookID     uint      `gorm:"uniqueIndex:uk_book_analytics_bucket;index;not null" json:"book_id"`
	DocumentID uint      `gorm:"uniqueIndex:uk_book_analytics_bucket;index;not null;default:0" json:"document_id"`
	Day        string    `gorm:"size:10;uniqueIndex:uk_book_analytics_bucket;index;not null" json:"day"`
	Source     string    `gorm:"size:20;uniqueIndex:uk_book_analytics_bucket;not null" json:"source"`
	ViewCount  int64     `gorm:"not null;default:0" json:"view_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Plugin 后台可安装插件（如 PDF 导出依赖的无头 Chrome）；未安装则相关功能不可用
type Plugin struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Key         string     `gorm:"size:50;uniqueIndex;not null" json:"key"` // 如 pdf-export
	Installed   bool       `gorm:"default:false" json:"installed"`
	Version     string     `gorm:"size:50" json:"version"`
	Meta        string     `json:"meta"` // JSON：如 {"chrome_path":"...","status":"downloading"}
	InstalledAt *time.Time `json:"installed_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// BookExportSetting 书籍自有导出（PDF）样式，每书一条；作者共享样式时优先于个人样式
type BookExportSetting struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	BookID       uint   `gorm:"uniqueIndex;not null" json:"book_id"`
	PageSize     string `gorm:"size:10;default:A4" json:"page_size"`
	IncludeCover bool   `gorm:"default:true" json:"include_cover"`
	IncludeToc   bool   `gorm:"default:true" json:"include_toc"`
	FontSize     int    `gorm:"default:15" json:"font_size"`
	CodeTheme    string `gorm:"size:20;default:light" json:"code_theme"`
	Margin       string `gorm:"size:10;default:normal" json:"margin"`
	// Footer 每页页脚文案（Powered by …）；空表示使用默认 "Powered by <站点名>"
	Footer    string    `gorm:"size:200;default:''" json:"footer"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BookExportRecord 用户导出书籍的历史记录（每导出一本书一条），用于「我的导出」列表。
// BookTitle 为导出当时的书名快照，避免原书改名/删除后历史丢失可读信息。
type BookExportRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	BookID    uint      `gorm:"index;not null" json:"book_id"`
	BookTitle string    `gorm:"size:255" json:"book_title"`
	BookSlug  string    `gorm:"size:255" json:"book_slug"`
	Format    string    `gorm:"size:16;not null" json:"format"` // markdown | pdf | docx | epub | zip
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// UserExportSetting 用户导出（PDF）样式偏好，每用户一条
type UserExportSetting struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	UserID       uint   `gorm:"uniqueIndex;not null" json:"user_id"`
	PageSize     string `gorm:"size:10;default:A4" json:"page_size"` // A4 | Letter
	IncludeCover bool   `gorm:"default:true" json:"include_cover"`
	IncludeToc   bool   `gorm:"default:true" json:"include_toc"`
	FontSize     int    `gorm:"default:15" json:"font_size"`             // 正文字号 px
	CodeTheme    string `gorm:"size:20;default:light" json:"code_theme"` // light | dark
	Margin       string `gorm:"size:10;default:normal" json:"margin"`    // narrow | normal | wide
	// Footer 每页页脚文案（Powered by …）；空表示使用默认 "Powered by <站点名>"
	Footer    string    `gorm:"size:200;default:''" json:"footer"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserReadingGoal 用户每日阅读目标（打卡日历用），每用户一条。
// GoalType 决定达标指标：chapters=每日新读章节数≥DailyChapters；minutes=每日阅读时长≥DailyMinutes。
type UserReadingGoal struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"uniqueIndex;not null" json:"user_id"`
	GoalType      string    `gorm:"size:16;default:chapters" json:"goal_type"` // chapters | minutes
	DailyChapters int       `gorm:"default:1" json:"daily_chapters"`
	DailyMinutes  int       `gorm:"default:15" json:"daily_minutes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ReadingDailyTime 用户每日累计阅读时长（秒），每用户每天一条，支撑「分钟制」每日目标与打卡。
type ReadingDailyTime struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:uk_reading_day;not null" json:"user_id"`
	Day       string    `gorm:"size:10;uniqueIndex:uk_reading_day;not null" json:"day"` // YYYY-MM-DD（服务器时区）
	Seconds   int       `gorm:"default:0" json:"seconds"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserThemeSetting 用户主题设置，每用户一条
type UserThemeSetting struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	UserID              uint      `gorm:"uniqueIndex;not null" json:"user_id"`
	PrimaryHue          string    `gorm:"size:20;default:blue" json:"primary_hue"`         // blue | indigo | violet | emerald | rose | amber | custom
	CustomColor         string    `gorm:"size:20;default:''" json:"custom_color"`          // hex color when primary_hue = custom
	Radius              string    `gorm:"size:10;default:lg" json:"radius"`                // sm | md | lg | xl | 2xl | custom
	CustomRadius        string    `gorm:"size:20;default:''" json:"custom_radius"`         // CSS value when radius = custom
	ButtonSize          string    `gorm:"size:10;default:md" json:"button_size"`           // sm | md | lg | custom
	CustomControlHeight string    `gorm:"size:20;default:''" json:"custom_control_height"` // CSS value when button_size = custom
	FontSize            string    `gorm:"size:10;default:15" json:"font_size"`             // 14 | 15 | 16 | custom
	CustomFontSize      string    `gorm:"size:20;default:''" json:"custom_font_size"`      // CSS value when font_size = custom
	ContentWidth        string    `gorm:"size:10;default:normal" json:"content_width"`     // narrow | normal | wide | custom
	CustomContentWidth  string    `gorm:"size:20;default:''" json:"custom_content_width"`  // CSS value when content_width = custom
	NavHeight           string    `gorm:"size:10;default:64" json:"nav_height"`            // 56 | 64 | 72 | custom
	CustomNavHeight     string    `gorm:"size:20;default:''" json:"custom_nav_height"`     // CSS value when nav_height = custom
	SidebarWidth        string    `gorm:"size:10;default:260" json:"sidebar_width"`        // 220 | 260 | 300 | custom
	CustomSidebarWidth  string    `gorm:"size:20;default:''" json:"custom_sidebar_width"`  // CSS value when sidebar_width = custom
	PageBg              string    `gorm:"size:20;default:#F7F6F2" json:"page_bg"`          // hex color | custom
	CustomPageBg        string    `gorm:"size:20;default:''" json:"custom_page_bg"`        // hex color when page_bg = custom
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// BookTag 书籍-标签联接表
type BookTag struct {
	BookID    uint      `gorm:"primaryKey" json:"book_id"`
	TagID     uint      `gorm:"primaryKey;index" json:"tag_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Document 文档，支持 parent_id 构成树形结构
type Document struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	BookID    uint   `gorm:"index;not null;uniqueIndex:uk_book_slug" json:"book_id"`
	ParentID  *uint  `gorm:"index" json:"parent_id"`
	Title     string `gorm:"size:255;not null" json:"title"`
	Slug      string `gorm:"size:255;not null;uniqueIndex:uk_book_slug" json:"slug"`
	Content   string `json:"content"`
	UserID    uint   `gorm:"index;not null" json:"user_id"`
	SortOrder int    `gorm:"default:0" json:"sort_order"`
	ViewCount int    `gorm:"default:0" json:"view_count"`
	Status    string `gorm:"size:20;default:draft;index" json:"status"`
	// Icon 目录树图标：从正文 <!-- icon: xxx --> 元数据提取的 FontAwesome 图标名，替换默认文档/文件夹图标
	Icon string `gorm:"size:64" json:"icon"`
	// ExternalURL 外链章节：非空时该章节是一个跳转到外部地址的链接，不渲染正文内容；
	// 写作端可只填地址不填编辑器内容。
	ExternalURL string `gorm:"size:1024" json:"external_url"`
	// ExternalNewTab 外链章节打开方式：true=新标签/新窗口（默认），false=当前窗口。
	// 指针型：保证显式 false 能写入（列默认 true）。
	ExternalNewTab *bool `gorm:"default:true" json:"external_new_tab"`
	// 公开后允许评论；指针型保证显式 false 能写入（列默认 true）
	AllowComments *bool          `gorm:"default:true" json:"allow_comments"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
	DeletedBy     uint           `gorm:"index;default:0" json:"-"`
	TrashGroup    string         `gorm:"size:64;index" json:"-"`
	Children      []*Document    `gorm:"-" json:"children,omitempty"`
	// PublishHeld 非持久化：本次保存中「发布」被发布守卫（如内容审核）拦截时的说明，章节保持未发布
	PublishHeld string `gorm:"-" json:"publish_held,omitempty"`
	// Paywall 非持久化：读者无权阅读全文（如付费内容未解锁）时由内容门禁给出的付费墙信息，此时 Content 为试读内容
	Paywall map[string]any `gorm:"-" json:"paywall,omitempty"`
	// ContentHash 非持久化：标题与正文的摘要，写作台保存时回传，用于发现他人在此期间的修改（见 DocumentHash）
	ContentHash string `gorm:"-" json:"content_hash,omitempty"`
}

// UserFile 用户上传到站点存储的文件（上传接口、Markdown 导入的图片、外链图片本地化），用于统计个人存储用量与删除。
// Driver/Name 记录保存时的驱动与文件名，站点之后切换存储驱动也能删除。
type UserFile struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Driver    string    `gorm:"size:10" json:"driver"`
	Name      string    `gorm:"size:255" json:"name"`
	URL       string    `gorm:"size:500;index" json:"url"`
	Ext       string    `gorm:"size:10" json:"ext"`
	Size      int64     `json:"size"`
	Source    string    `gorm:"size:20" json:"source"` // upload | markdown_import | image_localize
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// DocumentRevision 章节不可变历史版本。只允许新增与读取，不提供更新接口。
type DocumentRevision struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DocumentID    uint      `gorm:"index;not null" json:"document_id"`
	BookID        uint      `gorm:"index;not null" json:"book_id"`
	UserID        uint      `gorm:"index;not null" json:"user_id"`
	Title         string    `gorm:"size:255;not null" json:"title"`
	Content       string    `json:"content"`
	Status        string    `gorm:"size:20;not null" json:"status"`
	AllowComments bool      `gorm:"not null;default:true" json:"allow_comments"`
	Reason        string    `gorm:"size:20;not null" json:"reason"` // create | save | publish | pre_restore | restore
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

// All 执行多数据库迁移
// PersonalAccessToken 个人访问令牌：用于脚本、CI 等调用 API（Authorization: Bearer kf_pat_…）。
// 只保存令牌的 SHA-256 摘要；Prefix 为令牌开头若干字符，便于用户辨认。令牌不具备管理员权限，也不能访问账号安全相关接口。
type PersonalAccessToken struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	UserID    uint   `gorm:"index;not null" json:"user_id"`
	Name      string `gorm:"size:100" json:"name"`
	Prefix    string `gorm:"size:20" json:"prefix"`
	TokenHash string `gorm:"size:64;uniqueIndex" json:"-"`
	Scope     string `gorm:"size:10" json:"scope"` // all（全部权限）| custom（Permissions 所列权限）；旧令牌为 read（只读）| write（等同 all）
	// Permissions scope=custom 时令牌可用的权限（resource:action），见 app/access_tokens.go
	Permissions []string `gorm:"type:text;serializer:json" json:"permissions"`
	// BookID 限定书籍：非 0 时令牌只能操作这本书（且只能调用书籍相关的权限）
	BookID     uint       `gorm:"index;default:0" json:"book_id"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastUsedIP string     `gorm:"size:64" json:"last_used_ip"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CoreModels 核心数据表对应的模型（插件的表由各插件在启用时建表，见 plugins.Meta.Models）。
func CoreModels() []any {
	return []any{
		&I18nConfig{}, &SiteLocale{}, &UIMessageBundle{}, &LocalizedResourceContent{},
		&User{},
		&UserAuthentication{},
		&SiteConfig{},
		&AuditLog{},
		&Book{},
		&Document{},
		&DocumentRevision{},
		// Tag / BookTag 由「标签」插件在启用时建表（不在核心 AutoMigrate）。
		&ReadingProgress{},
		&ReadChapter{},
		&ReadingAnnotation{},
		&BookAnalyticsDaily{},
		&Plugin{},
		&UserExportSetting{},
		&BookExportRecord{},
		&UserFile{},
		&PersonalAccessToken{},
		&UserReadingGoal{},
		&ReadingDailyTime{},
		&EmailVerificationToken{},
		&TwoFactorBackupCode{},
		&CaptchaChallenge{},
		&OAuthState{},
		&LoginChallenge{},
		&RateLimitCounter{},
		&TwoFactorStepUp{},
		&LoginLockout{},
		&UserNotificationPref{},
		&BookExportSetting{},
		&UserThemeSetting{},
		&Comment{},
		&BookReview{},
		&ContentReport{},
		&Reaction{},
		&Notification{},
		&BookCollaborator{},
		&PasswordResetToken{},
		&BackgroundJob{},
		&AIUsageLog{},
		&AIAlert{},
		&SystemBackup{},
		&WriterPresence{},
		&WriterComment{},
		&WriterSuggestion{},
		&WriterChapterTask{},
		&WriterActivity{},
		// 成就相关表由「成就」插件在启用时建表（首次启用才创建），不在核心 AutoMigrate 里。
	}
}

func All(db *gorm.DB) error {
	if err := db.AutoMigrate(CoreModels()...); err != nil {
		return err
	}
	if err := backfillAITraceIDs(db); err != nil {
		return err
	}
	return SeedI18n(db)
}
