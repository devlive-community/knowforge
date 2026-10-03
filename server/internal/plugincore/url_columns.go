package plugincore

// —— 文件地址所在的列：存储迁移把文件搬到新存储后，需要把这些列中出现的旧地址改为新地址。
// 核心与各插件登记自己表中可能含有上传文件地址的文本列（正文、封面、图标等）；表须有 id 主键。
// 表不存在（插件未启用或已卸载）时跳过。——

// URLColumns 一张表中可能含有文件地址的列。
type URLColumns struct {
	Table   string
	Columns []string
}

var urlColumns []URLColumns

// RegisterURLColumns 登记含有文件地址的列（在 init 中调用）。
func RegisterURLColumns(table string, columns ...string) {
	urlColumns = append(urlColumns, URLColumns{Table: table, Columns: columns})
}

// AllURLColumns 已登记的全部列。
func AllURLColumns() []URLColumns {
	return append([]URLColumns(nil), urlColumns...)
}
