package app

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 核心登记的权益：书籍数量上限、单本书协作者人数上限、单文件上传大小。
// 基础值默认与升级前一致（书籍/协作者不限；上传沿用「内容设置」中的上传大小），管理员主动收紧才生效。
// 插件登记各自的权益（如内容采集的单页/整站采集与页数上限），来源（成长等级、会员方案）由插件提供。

const (
	entBooksMax         = "books.max"
	entCollaboratorsMax = "collaborators.max"
	entUploadMaxMB      = "upload.max_mb"
	entCustomFooter     = "export.custom_footer"
	cfgCustomFooter     = "entitlement_export_custom_footer"

	cfgEntBooksMax         = "entitlement_books_max"
	cfgEntCollaboratorsMax = "entitlement_collaborators_max"
	maxUploadEntitlementMB = 1024
)

// settingLimit 读取数值型基础值（未设置或无效时返回 def）。
func settingLimit(core plugincore.Core, key string, def int64) int64 {
	raw := core.GetSetting(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entBooksMax, Kind: plugincore.EntitlementLimit, Unit: "books", Min: 0, Max: 100000, AllowUnlimited: true, Order: 10,
		Base: func(core plugincore.Core) int64 { return settingLimit(core, cfgEntBooksMax, plugincore.Unlimited) },
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgEntBooksMax, strconv.FormatInt(v, 10), "权益：书籍数量上限（基础）")
		},
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entCollaboratorsMax, Kind: plugincore.EntitlementLimit, Unit: "people", Min: 0, Max: 1000, AllowUnlimited: true, Order: 20,
		Base: func(core plugincore.Core) int64 {
			return settingLimit(core, cfgEntCollaboratorsMax, plugincore.Unlimited)
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgEntCollaboratorsMax, strconv.FormatInt(v, 10), "权益：单本书协作者人数上限（基础）")
		},
	})
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		// 基础值即「内容设置」中的上传大小（同一配置项，两处修改一致）；等级/会员可放宽到 1024MB
		Key: entUploadMaxMB, Kind: plugincore.EntitlementLimit, Unit: "mb", Min: 1, Max: maxUploadEntitlementMB, Order: 30,
		Base: func(core plugincore.Core) int64 {
			v := settingLimit(core, cfgUploadMaxMB, 10)
			if v < 1 {
				v = 1
			} else if v > 100 {
				v = 100
			}
			return v
		},
		SetBase: func(core plugincore.Core, v int64) error {
			if v > 100 {
				return fmt.Errorf("基础上传大小最大 100MB（更大的上限请通过等级或会员权益授予）")
			}
			return core.SetSetting(cfgUploadMaxMB, strconv.FormatInt(v, 10), "上传文件大小上限（MB）")
		},
	})
}

func init() {
	// 导出页脚自定义：关闭时导出文件的页脚固定为「Powered by 站点名」（相当于去除站点标识为权益）；基础为开，与升级前一致
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entCustomFooter, Kind: plugincore.EntitlementFlag, Order: 36,
		Base: func(core plugincore.Core) int64 {
			if core.GetSetting(cfgCustomFooter) == "0" {
				return 0
			}
			return 1
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgCustomFooter, strconv.FormatInt(v, 10), "权益：自定义导出页脚（基础）")
		},
	})
}

// entitlement 当前用户某项权益的生效值。
func (a *App) entitlement(u *models.User, key string) int64 {
	return plugincore.EntitlementValue(a, u, key)
}

// EnsureBookQuota 校验用户是否还能创建书籍（书籍数量上限，不含回收站中的书）；超限返回可展示的错误。
func (a *App) EnsureBookQuota(u *models.User) error {
	if u == nil {
		return nil
	}
	limit := a.entitlement(u, entBooksMax)
	if limit == plugincore.Unlimited {
		return nil
	}
	var count int64
	a.DB.Model(&models.Book{}).Where("user_id = ?", u.ID).Count(&count)
	if !plugincore.WithinLimit(limit, count) {
		return fmt.Errorf("已达到书籍数量上限（%d 本），升级等级或开通会员可创建更多书籍", limit)
	}
	return nil
}

// failBookQuota 书籍数量超限时写 403 并返回 true。
func (a *App) failBookQuota(c *gin.Context, u *models.User) bool {
	if err := a.EnsureBookQuota(u); err != nil {
		fail(c, http.StatusForbidden, err.Error())
		return true
	}
	return false
}

// userUploadMaxBytes 当前用户的单文件上传上限（字节）。
func (a *App) userUploadMaxBytes(u *models.User) int64 {
	mb := a.entitlement(u, entUploadMaxMB)
	if mb < 1 {
		mb = 1
	}
	return mb << 20
}

// entitlementValues 用户全部权益的生效值（键 → 值），随 /auth/me 下发供前端联动入口。
func (a *App) entitlementValues(u *models.User) map[string]int64 {
	resolved := plugincore.ResolveEntitlements(a, u)
	out := make(map[string]int64, len(resolved))
	for k, r := range resolved {
		out[k] = r.Value
	}
	return out
}

// EntitlementDefinitions GET /entitlements/definitions 权益定义（供等级/会员权益编辑器）。
func (a *App) EntitlementDefinitions(c *gin.Context) {
	ok(c, gin.H{"items": plugincore.Entitlements()})
}

// MyEntitlements GET /users/me/entitlements 当前用户各项权益的生效值与来源。
func (a *App) MyEntitlements(c *gin.Context) {
	resolved := plugincore.ResolveEntitlements(a, currentUser(c))
	items := make([]plugincore.ResolvedEntitlement, 0, len(resolved))
	for _, def := range plugincore.Entitlements() {
		if r, has := resolved[def.Key]; has {
			items = append(items, r)
		}
	}
	ok(c, gin.H{"items": items, "definitions": plugincore.Entitlements()})
}

type entitlementBaseItem struct {
	plugincore.EntitlementDef
	Base      int64 `json:"base"`
	Available bool  `json:"available"`
}

// AdminEntitlements GET /admin/entitlements 权益定义与基础值（全站默认）。
func (a *App) AdminEntitlements(c *gin.Context) {
	items := make([]entitlementBaseItem, 0)
	for _, def := range plugincore.Entitlements() {
		item := entitlementBaseItem{EntitlementDef: def, Available: def.Available == nil || def.Available(a)}
		if def.Base != nil {
			item.Base = def.Base(a)
		}
		items = append(items, item)
	}
	ok(c, gin.H{"items": items})
}

// AdminUpdateEntitlementBase PUT /admin/entitlements/base {values: {key: value}} 保存基础值（只改传入的键）。
func (a *App) AdminUpdateEntitlementBase(c *gin.Context) {
	var req struct {
		Values map[string]int64 `json:"values"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Values) == 0 {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	if err := plugincore.ValidateEntitlementMap(req.Values); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	changed := []string{}
	for _, def := range plugincore.Entitlements() {
		v, has := req.Values[def.Key]
		if !has || def.SetBase == nil {
			continue
		}
		if err := def.SetBase(a, v); err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		changed = append(changed, def.Key)
	}
	a.recordAudit(c, "entitlement.base_updated", "entitlement", "base", "基础权益", changedFields(changed...))
	a.AdminEntitlements(c)
}
