package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SpeechRequest 语音合成请求。
type SpeechRequest struct {
	Text  string
	Voice string
}

// SpeechResult 合成结果：音频数据与类型（如 audio/mpeg）。
type SpeechResult struct {
	Audio       []byte
	ContentType string
	Model       string
}

func (c Config) speechEndpoint() (base, key string, ok bool) {
	if strings.TrimSpace(c.SpeechModel) == "" {
		return "", "", false
	}
	base, key = strings.TrimSpace(c.SpeechBaseURL), strings.TrimSpace(c.SpeechAPIKey)
	if base == "" {
		if c.Provider == ProviderAnthropic {
			return "", "", false // Anthropic 没有语音合成接口，需单独配置
		}
		base = c.BaseURL
	}
	if key == "" && strings.TrimSpace(c.SpeechBaseURL) == "" {
		key = c.APIKey
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return base, key, key != "" || strings.Contains(strings.ToLower(base), "localhost") || strings.Contains(base, "127.0.0.1")
}

// SpeechAvailable 是否已配置语音合成服务。
func (c Config) SpeechAvailable() bool {
	_, _, ok := c.speechEndpoint()
	return ok
}

// Speech 调用 OpenAI 兼容的语音合成接口，返回 MP3 音频。
func Speech(ctx context.Context, cfg Config, req SpeechRequest) (SpeechResult, error) {
	base, key, ok := cfg.speechEndpoint()
	if !ok {
		return SpeechResult{}, errors.New("尚未配置语音合成服务")
	}
	if strings.TrimSpace(req.Text) == "" {
		return SpeechResult{}, errors.New("朗读内容为空")
	}
	raw, _ := json.Marshal(map[string]any{"model": cfg.SpeechModel, "input": req.Text, "voice": req.Voice, "response_format": "mp3"})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/audio/speech", bytes.NewReader(raw))
	if err != nil {
		return SpeechResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return SpeechResult{}, fmt.Errorf("调用语音合成服务失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return SpeechResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return SpeechResult{}, fmt.Errorf("语音合成服务返回 %d: %s", resp.StatusCode, msg)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "audio/") {
		ct = "audio/mpeg"
	}
	if len(data) == 0 {
		return SpeechResult{}, errors.New("语音合成服务返回了空音频")
	}
	return SpeechResult{Audio: data, ContentType: ct, Model: cfg.SpeechModel}, nil
}
