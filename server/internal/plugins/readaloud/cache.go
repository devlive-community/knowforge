package readaloud

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// —— 音频缓存：<数据目录>/plugin-data/read-aloud-cache/<摘要前两位>/<摘要>.mp3，摘要取自模型、音色与文字。
// 命中时刷新修改时间；总大小超过上限时按修改时间从旧到新删除到上限的 80%。多实例共享数据目录，因此也共享缓存。——

func cacheKey(model, voice, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + voice + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func (b *behavior) cachePath(key string) (string, error) {
	dir, err := b.core.PrivateDataDir(cacheDirName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, key[:2], key+".mp3"), nil
}

func (b *behavior) cacheGet(key string) ([]byte, bool) {
	path, err := b.cachePath(key)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return data, true
}

func (b *behavior) cachePut(key string, data []byte) {
	if b.cacheMaxBytes() == 0 {
		return // 上限为 0：不缓存
	}
	path, err := b.cachePath(key)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if os.WriteFile(tmp, data, 0o644) != nil || os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
		return
	}
	b.maybeEvict()
}

func (b *behavior) cacheMaxBytes() int64 {
	mb, err := strconv.ParseInt(strings.TrimSpace(b.core.GetSetting(cfgCacheMaxMB)), 10, 64)
	if err != nil || mb < 0 || mb > maxCacheMaxMB {
		mb = defaultCacheMaxMB
	}
	return mb << 20
}

var (
	evictMu   sync.Mutex
	lastEvict time.Time
)

// maybeEvict 写入新音频后检查缓存大小（每个进程每 10 分钟最多一次，在后台进行；多实例同时清理也无妨）。
func (b *behavior) maybeEvict() {
	evictMu.Lock()
	if time.Since(lastEvict) < 10*time.Minute {
		evictMu.Unlock()
		return
	}
	lastEvict = time.Now()
	evictMu.Unlock()
	go func() {
		if _, err := b.evict(b.cacheMaxBytes()); err != nil {
			log.Printf("AI 朗读：清理音频缓存失败: %v", err)
		}
	}()
}

type cacheFile struct {
	path string
	size int64
	mod  time.Time
}

func (b *behavior) cacheFiles() ([]cacheFile, int64, error) {
	dir, err := b.core.PrivateDataDir(cacheDirName)
	if err != nil {
		return nil, 0, err
	}
	var files []cacheFile
	var total int64
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".mp3") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, cacheFile{path, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	return files, total, err
}

// evict 总大小超过 limit 时删除最久未使用的音频，直到不超过 limit 的 80%；limit 为 0 时清空。返回删除的文件数。
func (b *behavior) evict(limit int64) (int, error) {
	files, total, err := b.cacheFiles()
	if err != nil || total <= limit && limit > 0 {
		return 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	target := limit * 8 / 10
	removed := 0
	for _, f := range files {
		if total <= target && limit > 0 {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			removed++
		}
	}
	return removed, nil
}

// inflight 同一段音频并发请求时只合成一次。
var inflight = struct {
	sync.Mutex
	m map[string]*sync.WaitGroup
}{m: map[string]*sync.WaitGroup{}}
