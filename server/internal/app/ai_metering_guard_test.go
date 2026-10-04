package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 所有模型调用都必须记录用量：只允许经 App.meteredChat / AIEmbed / AISpeech（AI 服务）与 translateGoogleMetered（Google 翻译）调用模型。
// 本测试扫描 internal 下的非测试源码，发现在允许位置之外直接调用 ai.Chat / ai.Embed 或直连模型接口即失败，
// 防止新功能绕过用量记录与额度。
func TestModelCallsAreMetered(t *testing.T) {
	allowed := map[string][]string{
		filepath.Join("ai", "ai.go"):                   nil, // 客户端实现本身
		filepath.Join("ai", "stream.go"):               nil,
		filepath.Join("ai", "speech.go"):               nil,
		filepath.Join("app", "ai_service.go"):          {"ai.Chat(", "ai.ChatStream(", "ai.Embed(", "ai.Speech("},
		filepath.Join("app", "handler_translation.go"): {"/language/translate/v2"},
	}
	patterns := []string{"ai.Chat(", "ai.ChatStream(", "ai.Embed(", "ai.Speech(", "/audio/speech", "/chat/completions", "/v1/messages", "/embeddings", "/language/translate/v2", "generativelanguage.googleapis.com"}
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if ok, listed := allowed[rel]; listed && ok == nil {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		for _, p := range patterns {
			if !strings.Contains(src, p) {
				continue
			}
			permitted := false
			for _, a := range allowed[rel] {
				if a == p {
					permitted = true
				}
			}
			if !permitted {
				t.Errorf("%s 直接调用了模型（%q），必须经 Core.AIChat / AIEmbed 或 App.meteredChat 以记录用量", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
