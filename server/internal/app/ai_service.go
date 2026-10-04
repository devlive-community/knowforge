package app

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/ai"
)

// AI 服务（系统设置 · AI 服务）：站点级的大模型配置，供插件（如问答）经 Core.AIChat / AIEmbed 调用。
// 未单独配置时沿用「翻译」设置中的 OpenAI / Claude 配置。密钥只写不读。

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

var aiSettingKeys = []struct {
	field, key, desc string
	secret           bool
}{
	{"provider", "ai_provider", "AI 服务：对话接口类型 openai|anthropic", false},
	{"base_url", "ai_base_url", "AI 服务：对话接口地址", false},
	{"api_key", "ai_api_key", "AI 服务：对话接口密钥", true},
	{"model", "ai_model", "AI 服务：对话模型", false},
	{"embed_base_url", "ai_embed_base_url", "AI 服务：向量嵌入接口地址（OpenAI 兼容）", false},
	{"embed_api_key", "ai_embed_api_key", "AI 服务：向量嵌入接口密钥", true},
	{"embed_model", "ai_embed_model", "AI 服务：向量嵌入模型", false},
	{"tts_base_url", "ai_tts_base_url", "AI 服务：语音合成接口地址（OpenAI 兼容）", false},
	{"tts_api_key", "ai_tts_api_key", "AI 服务：语音合成接口密钥", true},
	{"tts_model", "ai_tts_model", "AI 服务：语音合成模型", false},
	{"tts_voices", "ai_tts_voices", "AI 服务：可选的朗读音色（逗号分隔，第一个为默认）", false},
	{"price_currency", "ai_price_currency", "AI 服务：计费货币（估算费用用）", false},
	{"price_input", "ai_price_input", "AI 服务：对话输入单价（每百万 tokens）", false},
	{"price_output", "ai_price_output", "AI 服务：对话输出单价（每百万 tokens）", false},
	{"price_embed", "ai_price_embed", "AI 服务：向量嵌入单价（每百万 tokens）", false},
	{"price_translate", "ai_price_translate", "AI 服务：机器翻译单价（每百万字符，Google 翻译）", false},
	{"price_tts", "ai_price_tts", "AI 服务：语音合成单价（每百万字符）", false},
	{"alert_daily_cost", cfgAlertDailyCost, "AI 用量预警：全站当日估算费用阈值（0 为关闭）", false},
	{"alert_user_daily_tokens", cfgAlertUserDailyTokens, "AI 用量预警：单个用户当日 tokens 阈值（0 为关闭）", false},
	{"alert_trace_tokens", cfgAlertTraceTokens, "AI 用量预警：单条调用链 tokens 阈值（0 为关闭）", false},
	{"usage_retention_days", cfgAIUsageRetentionDays, "AI 用量记录保留天数（0 为永久，否则不少于 90）", false},
	{"show_user_cost", cfgAIShowUserCost, "AI 用量：在「我的 AI 用量」中向用户展示估算费用（false 为不展示）", false},
}

// aiConfig 当前生效的 AI 配置与来源（ai | translation | none）。
func (a *App) aiConfig() (ai.Config, string) {
	get := func(k string) string { return strings.TrimSpace(a.getSetting(k)) }
	cfg := ai.Config{
		Provider: get("ai_provider"), BaseURL: get("ai_base_url"), APIKey: get("ai_api_key"), Model: get("ai_model"),
		EmbedBaseURL: get("ai_embed_base_url"), EmbedAPIKey: get("ai_embed_api_key"), EmbedModel: get("ai_embed_model"),
		SpeechBaseURL: get("ai_tts_base_url"), SpeechAPIKey: get("ai_tts_api_key"), SpeechModel: get("ai_tts_model"),
	}
	if cfg.Provider != ai.ProviderAnthropic {
		cfg.Provider = ai.ProviderOpenAI
	}
	if cfg.ChatAvailable() {
		return cfg, "ai"
	}
	// 回退：翻译设置中的 OpenAI / Claude
	switch get("translation_provider") {
	case "openai", "claude":
		fallback := cfg
		fallback.Provider = ai.ProviderOpenAI
		if get("translation_provider") == "claude" {
			fallback.Provider = ai.ProviderAnthropic
		}
		fallback.BaseURL, fallback.APIKey, fallback.Model = get("translation_api_base"), get("translation_api_key"), get("translation_model")
		if fallback.ChatAvailable() {
			return fallback, "translation"
		}
	}
	return cfg, "none"
}

// —— Core 接口 ——

// AIChat / AIEmbed 调用前按 ctx 上标注的调用方（ai.WithCaller）判定每月额度，调用后记录用量。

func (a *App) AIChat(ctx context.Context, req ai.ChatRequest) (ai.ChatResponse, error) {
	cfg, _ := a.aiConfig()
	return a.meteredChat(ctx, cfg, req, 0)
}

// meteredChat 用指定配置调用对话模型：调用前判定调用方每月额度，调用后记录用量。
// 站点内所有对话模型调用（AI 服务、翻译等）都必须经过这里，保证用量记录完整。chars 为翻译类调用的原文字符数。
func (a *App) meteredChat(ctx context.Context, cfg ai.Config, req ai.ChatRequest, chars int64) (ai.ChatResponse, error) {
	return a.meteredChatStream(ctx, cfg, req, chars, nil)
}

// AIChatStream 同 AIChat，并流式回调生成的文本片段。
func (a *App) AIChatStream(ctx context.Context, req ai.ChatRequest, onDelta func(text string)) (ai.ChatResponse, error) {
	cfg, _ := a.aiConfig()
	return a.meteredChatStream(ctx, cfg, req, 0, onDelta)
}

// AITranslateStream 以 AI 服务翻译并按原文字符数 chars 记入翻译用量（调用方功能须为 translate.*，计入每月翻译字数）。
func (a *App) AITranslateStream(ctx context.Context, req ai.ChatRequest, chars int64, onDelta func(text string)) (ai.ChatResponse, error) {
	cfg, _ := a.aiConfig()
	return a.meteredChatStream(ctx, cfg, req, chars, onDelta)
}

// meteredChatStream onDelta 非空时以流式接口调用；计量与额度判定同 meteredChat。
func (a *App) meteredChatStream(ctx context.Context, cfg ai.Config, req ai.ChatRequest, chars int64, onDelta func(string)) (ai.ChatResponse, error) {
	caller := ai.CallerFrom(ctx)
	if err := a.checkAIQuota(caller); err != nil {
		return ai.ChatResponse{}, err
	}
	started := time.Now()
	var (
		res ai.ChatResponse
		err error
	)
	if onDelta != nil {
		res, err = ai.ChatStream(ctx, cfg, req, onDelta)
	} else {
		res, err = ai.Chat(ctx, cfg, req)
	}
	model := res.Model
	if model == "" {
		model = cfg.Model
	}
	a.recordAIUsage(caller, "chat", cfg.Provider, model, res.Usage, chars, time.Since(started), err)
	return res, err
}

func (a *App) AIEmbed(ctx context.Context, texts []string) ([][]float32, ai.Usage, error) {
	cfg, _ := a.aiConfig()
	caller := ai.CallerFrom(ctx)
	if err := a.checkAIQuota(caller); err != nil {
		return nil, ai.Usage{}, err
	}
	started := time.Now()
	vecs, usage, err := ai.Embed(ctx, cfg, texts)
	a.recordAIUsage(caller, "embed", ai.ProviderOpenAI, cfg.EmbedModel, usage, 0, time.Since(started), err)
	return vecs, usage, err
}

// defaultTTSVoices 未设置音色时的默认列表（OpenAI 语音合成内置音色）。
var defaultTTSVoices = []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}

// AISpeechInfo 语音合成的模型与可选音色（第一个为默认）；未配置语音合成时 model 为空。
func (a *App) AISpeechInfo() (model string, voices []string) {
	cfg, _ := a.aiConfig()
	if !cfg.SpeechAvailable() {
		return "", nil
	}
	for _, v := range strings.Split(a.getSetting("ai_tts_voices"), ",") {
		if v = strings.TrimSpace(v); v != "" && len(v) <= 64 {
			voices = append(voices, v)
		}
	}
	if len(voices) == 0 {
		voices = defaultTTSVoices
	}
	return cfg.SpeechModel, voices
}

// AISpeech 语音合成：按字符数记入用量（kind=tts，不计 tokens，因此不受每月 AI tokens 额度限制，
// 额度由调用方的功能权益控制，如「每月朗读字数」）。
func (a *App) AISpeech(ctx context.Context, req ai.SpeechRequest) (ai.SpeechResult, error) {
	cfg, _ := a.aiConfig()
	started := time.Now()
	res, err := ai.Speech(ctx, cfg, req)
	a.recordAIUsage(ai.CallerFrom(ctx), "tts", ai.ProviderOpenAI, cfg.SpeechModel, ai.Usage{}, int64(len([]rune(req.Text))), time.Since(started), err)
	return res, err
}

func (a *App) AICheckQuota(ctx context.Context) error { return a.checkAIQuota(ai.CallerFrom(ctx)) }

func (a *App) AIStatus() (chat, embed bool) {
	cfg, _ := a.aiConfig()
	return cfg.ChatAvailable(), cfg.EmbedAvailable()
}

// —— 管理端 ——

// AdminGetAI GET /admin/ai AI 服务配置（密钥只返回是否已配置）与生效来源。
func (a *App) AdminGetAI(c *gin.Context) {
	out := gin.H{}
	for _, s := range aiSettingKeys {
		v := strings.TrimSpace(a.getSetting(s.key))
		if s.secret {
			out[s.field+"_set"] = v != ""
		} else {
			out[s.field] = v
		}
	}
	if out["provider"] == "" {
		out["provider"] = ai.ProviderOpenAI
	}
	cfg, source := a.aiConfig()
	out["source"] = source
	out["chat_available"] = cfg.ChatAvailable()
	out["embed_available"] = cfg.EmbedAvailable()
	out["tts_available"] = cfg.SpeechAvailable()
	ok(c, out)
}

// AdminUpdateAI PUT /admin/ai 保存 AI 服务配置（只保存传入的字段；密钥传空串表示不修改，传 "-" 清除）。
func (a *App) AdminUpdateAI(c *gin.Context) {
	var req map[string]*string
	if c.ShouldBindJSON(&req) != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fields := []string{}
	for _, s := range aiSettingKeys {
		p, has := req[s.field]
		if !has || p == nil {
			continue
		}
		v := strings.TrimSpace(*p)
		if s.secret {
			if v == "" {
				continue
			}
			if v == "-" {
				v = ""
			}
		}
		if s.field == "provider" && v != ai.ProviderOpenAI && v != ai.ProviderAnthropic {
			fail(c, http.StatusBadRequest, "接口类型必须为 openai 或 anthropic")
			return
		}
		if (s.field == "base_url" || s.field == "embed_base_url" || s.field == "tts_base_url") && v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			fail(c, http.StatusBadRequest, "接口地址需以 http:// 或 https:// 开头")
			return
		}
		if strings.HasPrefix(s.field, "price_") && s.field != "price_currency" && v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 100000 {
				fail(c, http.StatusBadRequest, "单价需为 0 到 100000 之间的数字（每百万 tokens）")
				return
			}
		}
		if s.field == "alert_daily_cost" && v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || f > 1_000_000 {
				fail(c, http.StatusBadRequest, "费用预警阈值需为 0 到 1000000 之间的数字（0 为关闭）")
				return
			}
		}
		if s.field == "usage_retention_days" && v != "" {
			if n, err := strconv.Atoi(v); err != nil || (n != 0 && (n < minAIUsageRetentionDays || n > 3650)) {
				fail(c, http.StatusBadRequest, fmt.Sprintf("保留天数需为 0（永久）或 %d 到 3650 之间的整数", minAIUsageRetentionDays))
				return
			}
		}
		if (s.field == "alert_user_daily_tokens" || s.field == "alert_trace_tokens") && v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err != nil || n < 0 || n > 1_000_000_000 {
				fail(c, http.StatusBadRequest, "tokens 预警阈值需为 0 到 1000000000 之间的整数（0 为关闭）")
				return
			}
		}
		if s.field == "show_user_cost" && v != "" && v != "true" && v != "false" {
			fail(c, http.StatusBadRequest, "是否展示估算费用需为 true 或 false")
			return
		}
		if s.field == "price_currency" {
			v = strings.ToUpper(v)
			if v != "" && !currencyCode.MatchString(v) {
				fail(c, http.StatusBadRequest, "货币需为三位字母代码，如 USD、CNY")
				return
			}
		}
		if err := a.setSetting(s.key, v, s.desc); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, s.field)
	}
	a.recordAudit(c, "ai.settings_updated", "config", "ai", "AI 服务", changedFields(fields...))
	a.AdminGetAI(c)
}

// AdminTestAI POST /admin/ai/test {kind: chat|embed|tts} 用当前配置发一次最小请求，验证连通性。
func (a *App) AdminTestAI(c *gin.Context) {
	var req struct {
		Kind string `json:"kind"`
	}
	_ = c.ShouldBindJSON(&req)
	ctx := ai.WithCaller(c.Request.Context(), ai.Caller{UserID: currentUser(c).ID, Feature: "admin.test"})
	started := time.Now()
	if req.Kind == "tts" {
		model, voices := a.AISpeechInfo()
		if model == "" {
			fail(c, http.StatusBadRequest, "尚未配置语音合成服务")
			return
		}
		res, err := a.AISpeech(ctx, ai.SpeechRequest{Text: "KnowForge", Voice: voices[0]})
		if err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
		ok(c, gin.H{"bytes": len(res.Audio), "content_type": res.ContentType, "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}
	if req.Kind == "embed" {
		vecs, _, err := a.AIEmbed(ctx, []string{"KnowForge"})
		if err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
		ok(c, gin.H{"dimensions": len(vecs[0]), "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}
	res, err := a.AIChat(ctx, ai.ChatRequest{Messages: []ai.Message{{Role: "user", Content: "Reply with the single word: pong"}}, MaxTokens: 16})
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"reply": res.Content, "elapsed_ms": time.Since(started).Milliseconds()})
}
