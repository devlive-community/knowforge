package app

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"knowforge/server/internal/config"
	"knowforge/server/internal/database"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugins"
)

// —— 备份文件格式：zip 中包含 manifest.json、db/<表名>.jsonl（每行一条记录）与 files/uploads/…（可选，本地上传文件）。
// 数据按表逐行导出为 JSON，与数据库类型无关：可以恢复到 SQLite、MySQL 或 PostgreSQL 中的任意一种（也可借此更换数据库）。
// 只导出核心与插件模型对应的表；搜索索引、限流计数、验证码、登录挑战、后台任务等运行时数据不导出（恢复后自动重建或无需保留）。——

const backupFormat = 1

// backupExcludedTables 不导出的运行时表。
var backupExcludedTables = map[string]bool{
	"captcha_challenges": true, "o_auth_states": true, "login_challenges": true, "rate_limit_counters": true,
	"two_factor_step_ups": true, "login_lockouts": true, "background_jobs": true, "system_backups": true,
	"email_verification_tokens": true, "password_reset_tokens": true,
}

type backupManifest struct {
	Format       int               `json:"format"`
	AppVersion   string            `json:"app_version"`
	CreatedAt    time.Time         `json:"created_at"`
	DBType       string            `json:"db_type"`
	SiteName     string            `json:"site_name"`
	Secret       string            `json:"secret"`
	IncludeFiles bool              `json:"include_files"`
	Files        int               `json:"files"`
	Tables       []backupTableInfo `json:"tables"`
}

type backupTableInfo struct {
	Name   string   `json:"name"`
	Rows   int64    `json:"rows"`
	Binary []string `json:"binary,omitempty"` // 以 base64 保存的二进制列
}

// backupTable 一张可备份的表及其模型（用于建表、排序与恢复时的类型转换）。
type backupTable struct {
	Name   string
	Model  any
	Schema *schema.Schema
}

// backupModelTables 核心与全部插件模型对应的表（去重，排除运行时表），按外键依赖排序：被引用的表在前。
func backupModelTables(db *gorm.DB) ([]backupTable, error) {
	all := append([]any{}, models.CoreModels()...)
	for _, m := range plugins.All() {
		all = append(all, m.Models...)
	}
	seen := map[string]bool{}
	var tables []backupTable
	for _, model := range all {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return nil, fmt.Errorf("解析数据模型失败: %w", err)
		}
		name := stmt.Schema.Table
		if seen[name] || backupExcludedTables[name] {
			continue
		}
		seen[name] = true
		tables = append(tables, backupTable{Name: name, Model: model, Schema: stmt.Schema})
	}
	return orderBackupTables(tables), nil
}

// orderBackupTables 按外键依赖排序（拓扑序，保持原有相对顺序）：恢复时先写被引用的表，避免外键约束报错。
func orderBackupTables(tables []backupTable) []backupTable {
	index := map[string]int{}
	for i, t := range tables {
		index[t.Name] = i
	}
	deps := make([][]int, len(tables))
	for i, t := range tables {
		for _, rel := range t.Schema.Relationships.Relations {
			c := rel.ParseConstraint()
			if c == nil || c.Schema == nil || c.ReferenceSchema == nil || c.Schema.Table == c.ReferenceSchema.Table {
				continue
			}
			from, ok1 := index[c.Schema.Table]
			to, ok2 := index[c.ReferenceSchema.Table]
			if ok1 && ok2 {
				deps[from] = append(deps[from], to)
			}
		}
		_ = i
	}
	out := make([]backupTable, 0, len(tables))
	state := make([]int, len(tables)) // 0 未访问，1 访问中，2 完成
	var visit func(i int)
	visit = func(i int) {
		if state[i] != 0 {
			return // 已完成，或成环（忽略环上的边）
		}
		state[i] = 1
		for _, d := range deps[i] {
			visit(d)
		}
		state[i] = 2
		out = append(out, tables[i])
	}
	for i := range tables {
		visit(i)
	}
	return out
}

// —— 导出 ——

type backupProgress func(stage string, done, total int)

// backupStats 一次备份的统计。
type backupStats struct {
	Tables int
	Rows   int64
	Files  int
}

// writeBackupArchive 把站点数据写入 zip：先导出数据库（一致性快照），再（可选）打包本地上传文件，最后写入 manifest.json。
func (a *App) writeBackupArchive(ctx context.Context, w io.Writer, includeFiles bool, progress backupProgress) (backupStats, error) {
	zw := zip.NewWriter(w)
	tables, err := backupModelTables(a.DB)
	if err != nil {
		return backupStats{}, err
	}
	manifest := backupManifest{
		Format: backupFormat, AppVersion: Version, CreatedAt: time.Now().UTC(), DBType: a.DB.Dialector.Name(),
		SiteName: a.getSetting("site_name"), Secret: a.Config.Secret, IncludeFiles: includeFiles,
	}
	var stats backupStats
	err = a.withBackupSnapshot(ctx, func(src *gorm.DB) error {
		for i, t := range tables {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			progress("database", i, len(tables))
			if !src.Migrator().HasTable(t.Name) {
				continue
			}
			info, err := dumpBackupTable(ctx, src, t, zw)
			if err != nil {
				return fmt.Errorf("导出数据表 %s 失败: %w", t.Name, err)
			}
			manifest.Tables = append(manifest.Tables, info)
			stats.Tables++
			stats.Rows += info.Rows
		}
		progress("database", len(tables), len(tables))
		return nil
	})
	if err != nil {
		return stats, err
	}
	if includeFiles {
		n, err := writeBackupFiles(ctx, zw, progress)
		if err != nil {
			return stats, err
		}
		stats.Files, manifest.Files = n, n
	}
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return stats, err
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return stats, err
	}
	return stats, zw.Close()
}

// withBackupSnapshot 在一致的数据快照上导出：SQLite 先 VACUUM INTO 复制出快照文件再读取（不长时间占用写锁），
// MySQL / PostgreSQL 使用只读的可重复读事务。
func (a *App) withBackupSnapshot(ctx context.Context, fn func(src *gorm.DB) error) error {
	if a.DB.Dialector.Name() == "sqlite" {
		dir := filepath.Join(config.DataDir(), "backups")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		snapshot := filepath.Join(dir, fmt.Sprintf(".snapshot-%d.db", time.Now().UnixNano()))
		defer func() {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(snapshot + suffix)
			}
		}()
		if err := a.DB.WithContext(ctx).Exec("VACUUM INTO ?", snapshot).Error; err != nil {
			return fmt.Errorf("创建数据库快照失败: %w", err)
		}
		src, err := database.Open(config.DatabaseConfig{Type: database.TypeSQLite, Path: snapshot})
		if err != nil {
			return err
		}
		if sqlDB, err := src.DB(); err == nil {
			defer sqlDB.Close()
		}
		return fn(src.WithContext(ctx))
	}
	tx := a.DB.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	return fn(tx)
}

// dumpBackupTable 按主键顺序逐行导出一张表。
func dumpBackupTable(ctx context.Context, src *gorm.DB, t backupTable, zw *zip.Writer) (backupTableInfo, error) {
	info := backupTableInfo{Name: t.Name}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "db/" + t.Name + ".jsonl", Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return info, err
	}
	q := src.WithContext(ctx).Table(t.Name)
	if len(t.Schema.PrimaryFieldDBNames) > 0 {
		cols := make([]clause.OrderByColumn, 0, len(t.Schema.PrimaryFieldDBNames))
		for _, name := range t.Schema.PrimaryFieldDBNames {
			cols = append(cols, clause.OrderByColumn{Column: clause.Column{Name: name}})
		}
		q = q.Order(clause.OrderBy{Columns: cols})
	}
	rows, err := q.Rows()
	if err != nil {
		return info, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return info, err
	}
	binary := make([]bool, len(types))
	for i, ct := range types {
		if f := t.Schema.LookUpField(ct.Name()); f != nil && f.DataType == schema.Bytes {
			binary[i] = true
		} else {
			name := strings.ToUpper(ct.DatabaseTypeName())
			binary[i] = strings.Contains(name, "BLOB") || strings.Contains(name, "BYTEA") || strings.Contains(name, "BINARY")
		}
		if binary[i] {
			info.Binary = append(info.Binary, ct.Name())
		}
	}
	enc := json.NewEncoder(w)
	values := make([]any, len(types))
	ptrs := make([]any, len(types))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return info, err
		}
		rec := make(map[string]any, len(types))
		for i, ct := range types {
			rec[ct.Name()] = dumpValue(values[i], binary[i])
		}
		if err := enc.Encode(rec); err != nil {
			return info, err
		}
		info.Rows++
	}
	return info, rows.Err()
}

func dumpValue(v any, binary bool) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if binary {
			return base64.StdEncoding.EncodeToString(x)
		}
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return x
	}
}

// writeBackupFiles 打包本地上传目录（files/uploads/…）。
func writeBackupFiles(ctx context.Context, zw *zip.Writer, progress backupProgress) (int, error) {
	root := uploadsDir()
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("读取上传目录失败: %w", err)
	}
	for i, p := range paths {
		if ctx.Err() != nil {
			return i, ctx.Err()
		}
		if i%20 == 0 {
			progress("files", i, len(paths))
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return i, err
		}
		st, err := os.Stat(p)
		if err != nil {
			continue // 打包期间被删除的文件跳过
		}
		hdr := &zip.FileHeader{Name: "files/uploads/" + filepath.ToSlash(rel), Method: zip.Deflate, Modified: st.ModTime()}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return i, err
		}
		f, err := os.Open(p)
		if err != nil {
			return i, err
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return i, err
		}
	}
	progress("files", len(paths), len(paths))
	return len(paths), nil
}

// —— 恢复 ——

// readBackupManifest 打开备份文件并校验格式与版本：备份不能来自比当前更新的版本。
func readBackupManifest(path string) (*zip.ReadCloser, *backupManifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, errors.New("无法读取备份文件，请确认上传的是 KnowForge 备份（.zip）")
	}
	fail := func(err error) (*zip.ReadCloser, *backupManifest, error) {
		zr.Close()
		return nil, nil, err
	}
	f, err := zr.Open("manifest.json")
	if err != nil {
		return fail(errors.New("备份文件中缺少 manifest.json，不是有效的 KnowForge 备份"))
	}
	var m backupManifest
	err = json.NewDecoder(f).Decode(&m)
	f.Close()
	if err != nil || m.Format == 0 {
		return fail(errors.New("备份文件的 manifest.json 无效"))
	}
	if m.Format > backupFormat {
		return fail(fmt.Errorf("备份文件格式（%d）比当前版本支持的更新，请先升级 KnowForge", m.Format))
	}
	if versionLess(Version, m.AppVersion) {
		return fail(fmt.Errorf("备份来自更新的版本 %s，当前为 %s：请先升级到 %s 或更高版本再恢复", m.AppVersion, Version, m.AppVersion))
	}
	return zr, &m, nil
}

// restoreStats 恢复结果。
type restoreStats struct {
	Tables  int      `json:"tables"`
	Rows    int64    `json:"rows"`
	Files   int      `json:"files"`
	Skipped []string `json:"skipped"` // 当前版本中不存在的表（如已移除的插件）
}

// restoreBackupDatabase 把备份中的各表写入空的目标库：按依赖顺序建表（含插件表）、清空迁移时写入的种子数据后逐批写入，
// 全部写入在一个事务中完成；只写入目标表中存在的列；PostgreSQL 写入后重置自增序列。
func restoreBackupDatabase(ctx context.Context, db *gorm.DB, zr *zip.ReadCloser, m *backupManifest, progress backupProgress) (restoreStats, error) {
	stats := restoreStats{Skipped: []string{}}
	known, err := backupModelTables(db)
	if err != nil {
		return stats, err
	}
	inBackup := map[string]backupTableInfo{}
	for _, t := range m.Tables {
		inBackup[t.Name] = t
	}
	var tables []backupTable
	for _, t := range known {
		if _, ok := inBackup[t.Name]; ok {
			tables = append(tables, t)
		}
	}
	knownNames := map[string]bool{}
	for _, t := range known {
		knownNames[t.Name] = true
	}
	for _, t := range m.Tables {
		if !knownNames[t.Name] && !backupExcludedTables[t.Name] {
			stats.Skipped = append(stats.Skipped, t.Name)
		}
	}
	sort.Strings(stats.Skipped)
	for _, t := range tables {
		if !db.Migrator().HasTable(t.Name) {
			if err := db.AutoMigrate(t.Model); err != nil {
				return stats, fmt.Errorf("创建数据表 %s 失败: %w", t.Name, err)
			}
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先按依赖逆序清空（迁移时写入的种子数据），再按依赖顺序写入
		for i := len(tables) - 1; i >= 0; i-- {
			if err := tx.Exec("DELETE FROM ?", clause.Table{Name: tables[i].Name}).Error; err != nil {
				return fmt.Errorf("清空数据表 %s 失败: %w", tables[i].Name, err)
			}
		}
		for i, t := range tables {
			progress("database", i, len(tables))
			n, err := restoreBackupTable(tx, zr, t, inBackup[t.Name])
			if err != nil {
				return fmt.Errorf("恢复数据表 %s 失败: %w", t.Name, err)
			}
			stats.Tables++
			stats.Rows += n
		}
		progress("database", len(tables), len(tables))
		return nil
	})
	if err != nil {
		return stats, err
	}
	if db.Dialector.Name() == "postgres" {
		for _, t := range tables {
			// 只有自增主键 id 对应序列；pg_get_serial_sequence 对没有序列的列返回 NULL，setval 随之为空操作
			if pk := t.Schema.PrioritizedPrimaryField; pk != nil && pk.DBName == "id" && pk.AutoIncrement {
				db.Exec("SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE((SELECT MAX(id) FROM "+db.Statement.Quote(t.Name)+"), 0) + 1, false)", t.Name)
			}
		}
	}
	return stats, nil
}

func restoreBackupTable(tx *gorm.DB, zr *zip.ReadCloser, t backupTable, info backupTableInfo) (int64, error) {
	f, err := zr.Open("db/" + t.Name + ".jsonl")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	types, err := tx.Migrator().ColumnTypes(t.Name)
	if err != nil {
		return 0, err
	}
	columns := map[string]bool{}
	for _, ct := range types {
		columns[ct.Name()] = true
	}
	binary := map[string]bool{}
	for _, name := range info.Binary {
		binary[name] = true
	}
	batchSize := 5000 / max(1, len(columns)) // 控制单条语句的参数个数
	batchSize = max(1, min(batchSize, 500))
	batch := make([]map[string]any, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := tx.Table(t.Name).Create(&batch).Error
		batch = batch[:0]
		return err
	}
	dec := json.NewDecoder(f)
	dec.UseNumber()
	var n int64
	for dec.More() {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			return n, err
		}
		row := make(map[string]any, len(rec))
		for k, v := range rec {
			if !columns[k] {
				continue
			}
			val, err := restoreValue(v, t.Schema.LookUpField(k), binary[k])
			if err != nil {
				return n, fmt.Errorf("列 %s: %w", k, err)
			}
			row[k] = val
		}
		batch = append(batch, row)
		n++
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	return n, flush()
}

var restoreTimeLayouts = []string{
	time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02",
}

// restoreValue 按目标模型字段类型转换备份中的值（各数据库导出的布尔、时间、数字表示不同）。
func restoreValue(v any, f *schema.Field, binary bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	if s, ok := v.(string); ok && binary {
		return base64.StdEncoding.DecodeString(s)
	}
	num, isNum := v.(json.Number)
	if f == nil {
		if isNum {
			if i, err := num.Int64(); err == nil {
				return i, nil
			}
			return num.Float64()
		}
		return v, nil
	}
	switch f.DataType {
	case schema.Bool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case json.Number:
			return x.String() != "0", nil
		case string:
			return x == "1" || strings.EqualFold(x, "true") || strings.EqualFold(x, "t"), nil
		}
	case schema.Int, schema.Uint:
		switch x := v.(type) {
		case json.Number:
			if i, err := x.Int64(); err == nil {
				return i, nil
			}
			fl, err := x.Float64()
			return int64(fl), err
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		case string:
			return strconv.ParseInt(x, 10, 64)
		}
	case schema.Float:
		switch x := v.(type) {
		case json.Number:
			return x.Float64()
		case string:
			return strconv.ParseFloat(x, 64)
		}
	case schema.Time:
		if s, ok := v.(string); ok {
			for _, layout := range restoreTimeLayouts {
				if t, err := time.Parse(layout, s); err == nil {
					return t, nil
				}
			}
			return nil, fmt.Errorf("无法识别的时间 %q", s)
		}
	case schema.String:
		if isNum {
			return num.String(), nil
		}
		if b, ok := v.(bool); ok {
			return strconv.FormatBool(b), nil
		}
	case schema.Bytes:
		if s, ok := v.(string); ok {
			return []byte(s), nil
		}
	}
	if isNum {
		if i, err := num.Int64(); err == nil {
			return i, nil
		}
		return num.Float64()
	}
	return v, nil
}

// restoreBackupFiles 把备份中的本地上传文件解压到上传目录（拒绝越出目录的路径）。
func restoreBackupFiles(zr *zip.ReadCloser, progress backupProgress) (int, error) {
	root := uploadsDir()
	var entries []*zip.File
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "files/uploads/") && !f.FileInfo().IsDir() {
			entries = append(entries, f)
		}
	}
	for i, f := range entries {
		if i%20 == 0 {
			progress("files", i, len(entries))
		}
		rel := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(f.Name, "files/uploads/")))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return i, fmt.Errorf("备份中的文件路径无效: %s", f.Name)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return i, err
		}
		src, err := f.Open()
		if err != nil {
			return i, err
		}
		out, err := os.Create(dst)
		if err != nil {
			src.Close()
			return i, err
		}
		_, err = io.Copy(out, src)
		src.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return i, err
		}
		_ = os.Chtimes(dst, f.Modified, f.Modified)
	}
	progress("files", len(entries), len(entries))
	return len(entries), nil
}
