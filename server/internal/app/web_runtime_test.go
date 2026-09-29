package app

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"knowforge/server/internal/webbundle"
)

func TestEmbeddedWebRuntimeCanBePrepared(t *testing.T) {
	archive, err := webbundle.Open()
	if err != nil {
		t.Skip("development build has no embedded Web runtime")
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KNOWFORGE_DATA", t.TempDir())
	t.Setenv("KNOWFORGE_STATIC_ROOT", t.TempDir())
	t.Setenv("KNOWFORGE_WEB_PORT", "16900")
	web, err := prepareWebRuntime(16969)
	if err != nil {
		t.Fatal(err)
	}
	if web == nil || web.nodeVersion != "24.20.0" {
		t.Fatalf("unexpected embedded runtime: %#v", web)
	}
	if _, err := os.Stat(filepath.Join(web.runtimeDir, "server.js")); err != nil {
		t.Fatalf("embedded Next.js server is unavailable: %v", err)
	}
}

func TestSafeArchivePathRejectsTraversal(t *testing.T) {
	dst := t.TempDir()
	for _, name := range []string{"../escape", "nested/../../escape", "/absolute"} {
		if _, err := safeArchivePath(dst, name); err == nil {
			t.Fatalf("safeArchivePath(%q) should reject traversal", name)
		}
	}
	if path, err := safeArchivePath(dst, "node/bin/node"); err != nil || path != filepath.Join(dst, "node", "bin", "node") {
		t.Fatalf("safe path mismatch: path=%q err=%v", path, err)
	}
}

func TestExtractTarPreservesFilesAndLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix semantics")
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	entries := []struct {
		header tar.Header
		body   string
	}{
		{header: tar.Header{Name: "node/bin/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{header: tar.Header{Name: "node/bin/node-real", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}, body: "node"},
		{header: tar.Header{Name: "node/bin/node", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "node-real"}},
	}
	for _, entry := range entries {
		if err := tw.WriteHeader(&entry.header); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := extractTar(bytes.NewReader(archive.Bytes()), dst); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dst, "node", "bin", "node"))
	if err != nil || string(content) != "node" {
		t.Fatalf("extracted symlink content=%q err=%v", content, err)
	}
	info, err := os.Stat(filepath.Join(dst, "node", "bin", "node-real"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("executable mode missing: mode=%v err=%v", info.Mode(), err)
	}
}

func TestExtractTarRejectsWriteThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix semantics")
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	link := tar.Header{Name: "redirect", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "safe"}
	if err := tw.WriteHeader(&link); err != nil {
		t.Fatal(err)
	}
	file := tar.Header{Name: "redirect/file", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}
	if err := tw.WriteHeader(&file); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTar(bytes.NewReader(archive.Bytes()), t.TempDir()); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("expected symlink parent rejection, got %v", err)
	}
}

func TestMergeStaticAssetsDoesNotOverwriteExisting(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "chunks", "new.js"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "existing.js"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "existing.js"), []byte("replace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeStaticAssets(src, dst); err != nil {
		t.Fatal(err)
	}
	existing, _ := os.ReadFile(filepath.Join(dst, "existing.js"))
	added, _ := os.ReadFile(filepath.Join(dst, "chunks", "new.js"))
	if string(existing) != "old" || string(added) != "new" {
		t.Fatalf("unexpected merged files: existing=%q added=%q", existing, added)
	}
}

func TestWithEnvironmentReplacesValues(t *testing.T) {
	env := withEnvironment([]string{"PORT=3000", "KEEP=value"}, map[string]string{"PORT": "6900", "NODE_ENV": "production"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "PORT=3000") || !strings.Contains(joined, "PORT=6900") || !strings.Contains(joined, "KEEP=value") {
		t.Fatalf("unexpected environment: %v", env)
	}
}

func TestCleanupStaleWebRuntimesKeepsOnlyActiveManagedDirectory(t *testing.T) {
	runtimeRoot := t.TempDir()
	activeName := "0123456789abcdef"
	staleNames := []string{
		"1111111111111111",
		"2222222222222222.tmp-1234",
	}
	keptNames := []string{
		activeName,
		"manual-backup",
		"3333333333333333.tmp-not-a-pid",
		"ABCDEF0123456789",
	}
	for _, name := range append(staleNames, keptNames...) {
		if err := os.MkdirAll(filepath.Join(runtimeRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := cleanupStaleWebRuntimes(runtimeRoot, filepath.Join(runtimeRoot, activeName))
	if err != nil {
		t.Fatal(err)
	}
	if removed != len(staleNames) {
		t.Fatalf("removed %d directories, want %d", removed, len(staleNames))
	}
	for _, name := range staleNames {
		if _, err := os.Stat(filepath.Join(runtimeRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("stale runtime %q still exists: %v", name, err)
		}
	}
	for _, name := range keptNames {
		if _, err := os.Stat(filepath.Join(runtimeRoot, name)); err != nil {
			t.Fatalf("kept directory %q is unavailable: %v", name, err)
		}
	}
}
