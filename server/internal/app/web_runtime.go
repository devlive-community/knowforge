package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"knowforge/server/internal/config"
	"knowforge/server/internal/webbundle"
)

const defaultWebPort = 6900

// webRuntime is the embedded Node.js + Next.js process managed by the Go
// service. Users still deploy and supervise a single KnowForge binary.
type webRuntime struct {
	runtimeDir  string
	nodePath    string
	nodeVersion string
	port        int
	proxy       *httputil.ReverseProxy

	cmd     *exec.Cmd
	done    chan struct{}
	errMu   sync.Mutex
	waitErr error
	running atomic.Bool
}

func prepareWebRuntime(apiPort int) (*webRuntime, error) {
	archive, err := webbundle.Open()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("打开内嵌 Web 运行时失败: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, archive); err != nil {
		_ = archive.Close()
		return nil, fmt.Errorf("计算内嵌 Web 运行时摘要失败: %w", err)
	}
	_ = archive.Close()
	bundleID := fmt.Sprintf("%x", hash.Sum(nil))[:16]

	runtimeRoot := filepath.Join(config.DataDir(), "web-runtime")
	runtimeDir := filepath.Join(runtimeRoot, bundleID)
	if err := ensureRuntimeExtracted(runtimeRoot, runtimeDir); err != nil {
		return nil, err
	}
	nodePath, nodeVersion, err := validateRuntime(runtimeDir)
	if err != nil {
		return nil, err
	}
	if staticRoot := os.Getenv("KNOWFORGE_STATIC_ROOT"); staticRoot != "" {
		if err := mergeStaticAssets(filepath.Join(runtimeDir, ".next", "static"), staticRoot); err != nil {
			return nil, fmt.Errorf("合并 Next.js 静态资源失败: %w", err)
		}
	}

	port := defaultWebPort
	if raw := os.Getenv("KNOWFORGE_WEB_PORT"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 || parsed > 65535 {
			return nil, fmt.Errorf("无效的 KNOWFORGE_WEB_PORT: %q", raw)
		}
		port = parsed
	}
	if port == apiPort {
		return nil, fmt.Errorf("Web 内部端口不能与 API 端口相同: %d", port)
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
		log.Printf("Next.js 代理失败: %v", proxyErr)
		http.Error(w, "Web 页面暂时不可用", http.StatusServiceUnavailable)
	}

	return &webRuntime{
		runtimeDir:  runtimeDir,
		nodePath:    nodePath,
		nodeVersion: nodeVersion,
		port:        port,
		proxy:       proxy,
		done:        make(chan struct{}),
	}, nil
}

func ensureRuntimeExtracted(runtimeRoot, runtimeDir string) error {
	if _, _, err := validateRuntime(runtimeDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(runtimeRoot, 0o755); err != nil {
		return fmt.Errorf("创建 Web 运行时目录失败: %w", err)
	}
	tmpDir := fmt.Sprintf("%s.tmp-%d", runtimeDir, os.Getpid())
	_ = os.RemoveAll(tmpDir)
	defer os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("创建 Web 运行时临时目录失败: %w", err)
	}

	archive, err := webbundle.Open()
	if err != nil {
		return fmt.Errorf("重新打开内嵌 Web 运行时失败: %w", err)
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("读取内嵌 Web 运行时失败: %w", err)
	}
	defer gz.Close()
	if err := extractTar(gz, tmpDir); err != nil {
		return fmt.Errorf("解压内嵌 Web 运行时失败: %w", err)
	}
	if _, _, err := validateRuntime(tmpDir); err != nil {
		return err
	}
	_ = os.RemoveAll(runtimeDir)
	if err := os.Rename(tmpDir, runtimeDir); err != nil {
		return fmt.Errorf("激活 Web 运行时失败: %w", err)
	}
	return nil
}

func validateRuntime(runtimeDir string) (nodePath, nodeVersion string, err error) {
	serverPath := filepath.Join(runtimeDir, "server.js")
	if stat, statErr := os.Stat(serverPath); statErr != nil || !stat.Mode().IsRegular() {
		return "", "", fmt.Errorf("Web 运行时缺少 server.js")
	}
	versionRaw, err := os.ReadFile(filepath.Join(runtimeDir, ".knowforge-node-version"))
	if err != nil {
		return "", "", fmt.Errorf("Web 运行时缺少 Node.js 版本标记: %w", err)
	}
	nodeVersion = strings.TrimSpace(string(versionRaw))
	major, err := strconv.Atoi(strings.Split(nodeVersion, ".")[0])
	if err != nil || major < 24 {
		return "", "", fmt.Errorf("Web 运行时必须使用 Node.js 24+，当前标记为 %q", nodeVersion)
	}
	nodePath = filepath.Join(runtimeDir, "node", "bin", "node")
	if stat, statErr := os.Stat(nodePath); statErr != nil || stat.Mode()&0o111 == 0 {
		return "", "", fmt.Errorf("Web 运行时缺少可执行的 Node.js")
	}
	out, err := exec.Command(nodePath, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("Node.js 运行时自检失败: %w", err)
	}
	if strings.TrimSpace(string(out)) != "v"+nodeVersion {
		return "", "", fmt.Errorf("Node.js 运行时版本不匹配: 期望 v%s，得到 %s", nodeVersion, strings.TrimSpace(string(out)))
	}
	return nodePath, nodeVersion, nil
}

func (w *webRuntime) Start(apiPort int) error {
	if w == nil {
		return nil
	}
	cmd := exec.Command(w.nodePath, "server.js")
	cmd.Dir = w.runtimeDir
	cmd.Env = withEnvironment(os.Environ(), map[string]string{
		"NODE_ENV":          "production",
		"PORT":              strconv.Itoa(w.port),
		"HOSTNAME":          "127.0.0.1",
		"KNOWFORGE_API_URL": fmt.Sprintf("http://127.0.0.1:%d", apiPort),
	})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动内嵌 Next.js 失败: %w", err)
	}
	w.cmd = cmd
	w.running.Store(true)
	log.Printf("内嵌 Web 已启动: Node.js %s, http://127.0.0.1:%d", w.nodeVersion, w.port)
	go func() {
		removed, err := cleanupStaleWebRuntimes(filepath.Dir(w.runtimeDir), w.runtimeDir)
		if err != nil {
			log.Printf("清理旧 Web 运行时未完全成功（已删除 %d 个）: %v", removed, err)
			return
		}
		if removed > 0 {
			log.Printf("已清理 %d 个旧 Web 运行时目录", removed)
		}
	}()
	go func() {
		err := cmd.Wait()
		w.errMu.Lock()
		w.waitErr = err
		w.errMu.Unlock()
		w.running.Store(false)
		close(w.done)
	}()
	return nil
}

func cleanupStaleWebRuntimes(runtimeRoot, activeRuntimeDir string) (int, error) {
	entries, err := os.ReadDir(runtimeRoot)
	if err != nil {
		return 0, err
	}
	activeName := filepath.Base(filepath.Clean(activeRuntimeDir))
	removed := 0
	var cleanupErr error
	for _, entry := range entries {
		name := entry.Name()
		if name == activeName || !entry.IsDir() || !isManagedWebRuntimeDir(name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(runtimeRoot, name)); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("删除 %s: %w", name, err))
			continue
		}
		removed++
	}
	return removed, cleanupErr
}

func isManagedWebRuntimeDir(name string) bool {
	bundleID := name
	if before, after, found := strings.Cut(name, ".tmp-"); found {
		if after == "" {
			return false
		}
		for _, ch := range after {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		bundleID = before
	}
	if len(bundleID) != 16 {
		return false
	}
	for _, ch := range bundleID {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (w *webRuntime) Stop(ctx context.Context) {
	if w == nil || w.cmd == nil || w.cmd.Process == nil || !w.running.Load() {
		return
	}
	_ = w.cmd.Process.Signal(os.Interrupt)
	select {
	case <-w.done:
	case <-ctx.Done():
		_ = w.cmd.Process.Kill()
		<-w.done
	}
}

func (w *webRuntime) Wait() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}

func (w *webRuntime) Err() error {
	if w == nil {
		return nil
	}
	w.errMu.Lock()
	defer w.errMu.Unlock()
	return w.waitErr
}

func (w *webRuntime) Status() (status, nodeVersion string) {
	if w == nil {
		return "not_embedded", ""
	}
	if w.running.Load() {
		return "up", w.nodeVersion
	}
	return "down", w.nodeVersion
}

// Port 返回内嵌 Web 的本地监听端口（PDF 导出需内部访问打印页）；未嵌入返回 0
func (w *webRuntime) Port() int {
	if w == nil || !w.running.Load() {
		return 0
	}
	return w.port
}

func withEnvironment(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		key := strings.SplitN(item, "=", 2)[0]
		if _, replaced := values[key]; !replaced {
			result = append(result, item)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func extractTar(reader io.Reader, dst string) error {
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchivePath(dst, header.Name)
		if err != nil {
			return err
		}
		if err := ensureNoSymlinkParents(dst, target); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := validateLinkTarget(dst, target, header.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(header.Linkname), target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, err := safeArchivePath(dst, header.Linkname)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		}
	}
}

func ensureNoSymlinkParents(dst, target string) error {
	rel, err := filepath.Rel(dst, target)
	if err != nil {
		return err
	}
	current := dst
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("归档路径经过符号链接: %s", rel)
		}
	}
	return nil
}

func safeArchivePath(dst, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法的归档路径: %s", name)
	}
	target := filepath.Join(dst, clean)
	rel, err := filepath.Rel(dst, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法的归档路径: %s", name)
	}
	return target, nil
}

func validateLinkTarget(dst, linkPath, linkName string) error {
	linkName = filepath.FromSlash(linkName)
	if filepath.IsAbs(linkName) {
		return fmt.Errorf("非法的绝对符号链接: %s", linkName)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), linkName))
	rel, err := filepath.Rel(dst, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("符号链接越过运行时目录: %s", linkName)
	}
	return nil
}

func mergeStaticAssets(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inCloseErr := in.Close()
		outCloseErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inCloseErr != nil {
			return inCloseErr
		}
		return outCloseErr
	})
}

func shutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
