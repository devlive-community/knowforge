package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/config"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
	"knowforge/server/internal/storage"
)

// 个人存储：用户上传到站点存储的文件（上传接口、Markdown 导入的图片、外链图片本地化）逐个记录，
// 合计大小受权益 storage.total_mb 约束（基础不限）；「我的文件」可查看用量、文件被引用的情况并删除（同时从存储中删除）。
// 记录从本功能上线起开始，此前上传的文件不计入。

const (
	entStorageMB = "storage.total_mb"
	cfgStorageMB = "entitlement_storage_total_mb"
)

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entStorageMB, Kind: plugincore.EntitlementLimit, Unit: "mb", Min: 0, Max: 1_000_000, AllowUnlimited: true, Order: 31,
		Base: func(core plugincore.Core) int64 { return settingLimit(core, cfgStorageMB, plugincore.Unlimited) },
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgStorageMB, strconv.FormatInt(v, 10), "权益：个人存储空间（MB，基础）")
		},
	})
}

// errStorageFull 个人存储空间不足。
type errStorageFull struct{ used, limitMB int64 }

func (e errStorageFull) Error() string {
	return fmt.Sprintf("个人存储空间不足：已用 %s / %d MB，可在「我的文件」删除不用的文件，或升级等级、开通会员获得更多空间", formatMB(e.used), e.limitMB)
}

func formatMB(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/(1<<20), 'f', 1, 64) + " MB"
}

// storageUsed 用户已用的存储（字节）。
func (a *App) storageUsed(userID uint) int64 {
	var total struct{ N int64 }
	a.DB.Model(&models.UserFile{}).Select("COALESCE(SUM(size), 0) AS n").Where("user_id = ?", userID).Scan(&total)
	return total.N
}

// ensureStorage 再保存 size 字节后是否超出个人存储空间。
func (a *App) ensureStorage(u *models.User, size int64) error {
	limit := a.entitlement(u, entStorageMB)
	if limit == plugincore.Unlimited {
		return nil
	}
	if used := a.storageUsed(u.ID); used+size > limit<<20 {
		return errStorageFull{used: used, limitMB: limit}
	}
	return nil
}

// storeUserFile 为用户保存一个文件并记入个人存储（u 为 nil 时只保存，如系统任务）；超出个人存储空间时返回 errStorageFull。
func (a *App) storeUserFile(u *models.User, source, ext string, data []byte) (string, error) {
	if u != nil {
		if err := a.ensureStorage(u, int64(len(data))); err != nil {
			return "", err
		}
	}
	name, err := uploadName(ext)
	if err != nil {
		return "", err
	}
	up := storage.FromSettings(a.DB, config.DataDir())
	url, err := up.Upload(name, data)
	if err != nil {
		return "", err
	}
	if u != nil {
		a.DB.Create(&models.UserFile{UserID: u.ID, Driver: storage.DriverName(up), Name: name, URL: url,
			Ext: strings.TrimPrefix(ext, "."), Size: int64(len(data)), Source: source})
	}
	return url, nil
}

// fileReferences 文件地址被多少处使用（章节正文、书籍封面与简介、用户头像）。
func (a *App) fileReferences(url string) int64 {
	like := "%" + escapeLike(url) + "%"
	var docs, books, users int64
	a.DB.Model(&models.Document{}).Where("content LIKE ? ESCAPE '!'", like).Count(&docs)
	a.DB.Model(&models.Book{}).Where("cover_image = ? OR description LIKE ? ESCAPE '!'", url, like).Count(&books)
	a.DB.Model(&models.User{}).Where("avatar = ?", url).Count(&users)
	return docs + books + users
}

// MyFiles GET /users/me/files?page=&page_size= 我的文件（新→旧）与存储用量：{items[{file, references}], total, page, page_size, used_bytes, limit_mb}。
func (a *App) MyFiles(c *gin.Context) {
	u := currentUser(c)
	page, pageSize := paginate(c)
	q := a.DB.Model(&models.UserFile{}).Where("user_id = ?", u.ID)
	var total int64
	q.Count(&total)
	var files []models.UserFile
	q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&files)
	items := make([]gin.H, 0, len(files))
	for _, f := range files {
		items = append(items, gin.H{"file": f, "references": a.fileReferences(f.URL)})
	}
	ok(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize,
		"used_bytes": a.storageUsed(u.ID), "limit_mb": a.entitlement(u, entStorageMB)})
}

// DeleteMyFile DELETE /users/me/files/:id 删除自己的文件（同时从存储中删除；正在使用的图片删除后将无法显示）。
func (a *App) DeleteMyFile(c *gin.Context) {
	u := currentUser(c)
	var f models.UserFile
	if a.DB.Where("id = ? AND user_id = ?", c.Param("id"), u.ID).First(&f).Error != nil {
		fail(c, http.StatusNotFound, "文件不存在")
		return
	}
	up := storage.ForDriver(a.DB, config.DataDir(), f.Driver)
	deleter, can := up.(storage.Deleter)
	if up == nil || !can {
		fail(c, http.StatusConflict, "保存该文件的存储配置已不可用，无法删除，请联系管理员")
		return
	}
	if err := deleter.Delete(f.Name); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	a.DB.Delete(&f)
	ok(c, gin.H{"deleted": true, "used_bytes": a.storageUsed(u.ID)})
}

// isStorageFull 错误是否为个人存储空间不足。
func isStorageFull(err error) bool {
	var full errStorageFull
	return errors.As(err, &full)
}
