package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"knowforge/server/internal/ai"
	"knowforge/server/internal/mdmask"
	"knowforge/server/internal/models"
	"knowforge/server/internal/plugincore"
)

// 翻译：后台配置翻译方式（Google / OpenAI 协议 / Claude 协议），写作台调用翻译当前内容。
// 凭据存站点配置表，仅管理员可读写，不出现在公开 /site（公开仅暴露是否启用）。
// 每次翻译都写入 AI 用量记录（功能 translate）：OpenAI/Claude 经 meteredChat 记 tokens 并受每月 AI 用量约束，
// Google 按字符记录；所有方式统一受「每月翻译字数」权益约束。

const (
	entTranslateMonthlyChars = "translate.monthly_chars"
	cfgTranslateMonthlyChars = "translate_monthly_chars"
	featureTranslate         = "translate"
)

func init() {
	plugincore.RegisterEntitlement(plugincore.EntitlementDef{
		Key: entTranslateMonthlyChars, Kind: plugincore.EntitlementLimit, Unit: "chars", Min: 0, Max: 1_000_000_000, AllowUnlimited: true, Order: 45,
		// 翻译服务已配置，或 AI 服务可用（插件的 AI 翻译功能如整本翻译同样按字数计量）
		Available: func(core plugincore.Core) bool {
			if p := core.GetSetting("translation_provider"); p != "" && p != "none" {
				return true
			}
			chat, _ := core.AIStatus()
			return chat
		},
		Base: func(core plugincore.Core) int64 {
			return settingLimit(core, cfgTranslateMonthlyChars, plugincore.Unlimited)
		},
		SetBase: func(core plugincore.Core, v int64) error {
			return core.SetSetting(cfgTranslateMonthlyChars, strconv.FormatInt(v, 10), "权益：每月翻译字数（基础）")
		},
	})
}

// translateMonthUsed 用户本月已翻译的原文字符数（仅成功的调用；含插件的翻译功能 translate.*，如整本 AI 翻译）。
func (a *App) translateMonthUsed(userID uint) int64 {
	var total struct{ N int64 }
	a.DB.Model(&models.AIUsageLog{}).Select("COALESCE(SUM(characters), 0) AS n").
		Where("user_id = ? AND (feature = ? OR feature LIKE ?) AND status = ? AND created_at >= ?", userID, featureTranslate, featureTranslate+".%", "ok", monthStart(time.Now())).Scan(&total)
	return total.N
}

// TranslateCharsLeft 用户本月剩余翻译字数（每月翻译字数权益；不限返回 -1）。
func (a *App) TranslateCharsLeft(u *models.User) int64 {
	limit, enforced := a.meteredLimit(u, entTranslateMonthlyChars)
	if !enforced {
		return -1
	}
	if left := limit - a.translateMonthUsed(u.ID); left > 0 {
		return left
	}
	return 0
}

var translateRefTypes = map[string]bool{"document": true, "book": true, "resource": true}

var translationProviders = map[string]bool{"": true, "none": true, "google": true, "openai": true, "claude": true}

func (a *App) translationProvider() string {
	p := a.getSetting("translation_provider")
	if p == "none" {
		return ""
	}
	return p
}

// translationEnabled 是否已配置可用的翻译方式（供公开 /site 暴露给写作台按钮）。
func (a *App) translationEnabled() bool {
	p := a.translationProvider()
	if p == "" {
		return false
	}
	return a.getSetting("translation_api_key") != ""
}

type translationConfigUpdate struct {
	Provider *string `json:"provider"`
	APIKey   *string `json:"api_key"`
	APIBase  *string `json:"api_base"`
	Model    *string `json:"model"`
}

// AdminGetTranslation GET /admin/translation 管理员读取翻译配置
func (a *App) AdminGetTranslation(c *gin.Context) {
	ok(c, gin.H{
		"provider": a.getSetting("translation_provider"),
		"api_key":  a.getSetting("translation_api_key"),
		"api_base": a.getSetting("translation_api_base"),
		"model":    a.getSetting("translation_model"),
	})
}

// AdminSaveTranslation PUT /admin/translation 管理员保存翻译配置
func (a *App) AdminSaveTranslation(c *gin.Context) {
	var req translationConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	fields := []string{}
	if req.Provider != nil {
		if !translationProviders[*req.Provider] {
			fail(c, http.StatusBadRequest, "翻译方式必须为 none、google、openai 或 claude")
			return
		}
		if err := a.setSetting("translation_provider", *req.Provider, "翻译方式 none|google|openai|claude"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "provider")
	}
	if req.APIKey != nil {
		if err := a.setSetting("translation_api_key", strings.TrimSpace(*req.APIKey), "翻译服务 API Key"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "api_key")
	}
	if req.APIBase != nil {
		if err := a.setSetting("translation_api_base", strings.TrimSpace(*req.APIBase), "翻译服务 API 地址（OpenAI/Claude 兼容端点）"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "api_base")
	}
	if req.Model != nil {
		if err := a.setSetting("translation_model", strings.TrimSpace(*req.Model), "翻译使用的模型（AI 方式）"); err != nil {
			fail(c, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		fields = append(fields, "model")
	}
	a.recordAudit(c, "translation.updated", "config", "translation", "翻译配置", map[string]any{"changed_fields": fields})
	ok(c, gin.H{"message": "已保存"})
}

// Translate POST /translate 用后台配置的翻译方式翻译一段文本
func (a *App) Translate(c *gin.Context) {
	provider := a.translationProvider()
	if provider == "" {
		fail(c, http.StatusForbidden, "站点未配置翻译方式")
		return
	}
	apiKey := a.getSetting("translation_api_key")
	if apiKey == "" {
		fail(c, http.StatusForbidden, "翻译服务未配置 API Key")
		return
	}
	var req struct {
		Text        string `json:"text"`
		TargetLang  string `json:"target_lang"`  // 语言代码，如 en / zh-CN（Google 使用）
		TargetLabel string `json:"target_label"` // 语言名称，如 English / 简体中文（AI 使用）
		SourceLang  string `json:"source_lang"`
		RefType     string `json:"ref_type"` // 可选：document | book | resource（用量记录的关联对象）
		RefID       uint   `json:"ref_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "参数错误")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	req.TargetLang = strings.TrimSpace(req.TargetLang)
	req.TargetLabel = strings.TrimSpace(req.TargetLabel)
	if req.Text == "" {
		fail(c, http.StatusBadRequest, "请提供待翻译文本")
		return
	}
	if req.TargetLang == "" && req.TargetLabel == "" {
		fail(c, http.StatusBadRequest, "请指定目标语言")
		return
	}
	// AI 用语言名称更自然，Google 需语言代码；互相回退
	aiTarget := req.TargetLabel
	if aiTarget == "" {
		aiTarget = req.TargetLang
	}
	googleTarget := req.TargetLang
	if googleTarget == "" {
		googleTarget = req.TargetLabel
	}
	chars := int64(utf8.RuneCountInString(req.Text))
	if chars > 20000 {
		fail(c, http.StatusBadRequest, "单次翻译文本过长（上限 20000 字）")
		return
	}
	u := currentUser(c)
	if limit, enforced := a.meteredLimit(u, entTranslateMonthlyChars); enforced {
		if left := limit - a.translateMonthUsed(u.ID); chars > left {
			if left < 0 {
				left = 0
			}
			fail(c, http.StatusTooManyRequests, fmt.Sprintf("本月翻译字数不足（剩余 %d 字），下月恢复，或提升等级/开通会员获得更多额度", left))
			return
		}
	}
	caller := ai.Caller{UserID: u.ID, Feature: featureTranslate}
	if translateRefTypes[req.RefType] {
		caller.RefType, caller.RefID = req.RefType, req.RefID
	}
	ctx := ai.WithCaller(c.Request.Context(), caller) // 不设整体超时：长文本翻译由模型决定耗时，用户离开页面即取消

	var (
		translated string
		err        error
	)
	base := a.getSetting("translation_api_base")
	model := a.getSetting("translation_model")
	// 不可翻译的 Markdown 语法（组件标签、::: 块、[children] 等宏、链接地址、代码……）替换为占位符后再翻译，
	// 译文占位符不一致时重试一次，仍不一致则报错，不返回被改坏的内容。
	masked := mdmask.Mask(req.Text, mdmask.Options{})
	if !mdmask.HasText(masked.Text) {
		ok(c, gin.H{"text": req.Text})
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		var raw string
		switch provider {
		case "google":
			raw, err = a.translateGoogleMetered(ctx, caller, apiKey, base, masked.Text, googleTarget, chars)
		case "openai", "claude":
			raw, err = a.translateAI(ctx, provider, apiKey, base, model, masked.Text, aiTarget, chars)
		default:
			err = errors.New("不支持的翻译方式")
		}
		if err != nil {
			break
		}
		if mdmask.CheckSegment(masked.Text, raw) == nil {
			if translated, err = masked.Restore(raw); err == nil {
				break
			}
		}
		err = mdmask.ErrPlaceholders
	}
	if errors.Is(err, ai.ErrQuotaExceeded) {
		fail(c, http.StatusTooManyRequests, err.Error())
		return
	}
	if err != nil {
		fail(c, http.StatusBadGateway, "翻译失败: "+err.Error())
		return
	}
	ok(c, gin.H{"text": translated})
}

// translateAI 用翻译设置中的 OpenAI / Claude 配置翻译（经 meteredChat 记录用量）。
func (a *App) translateAI(ctx context.Context, provider, apiKey, base, model, text, target string, chars int64) (string, error) {
	cfg := ai.Config{Provider: ai.ProviderOpenAI, BaseURL: base, APIKey: apiKey, Model: model}
	if provider == "claude" {
		cfg.Provider = ai.ProviderAnthropic
		if cfg.Model == "" {
			cfg.Model = "claude-3-5-sonnet-latest" // 与翻译设置页的默认值一致
		}
	}
	res, err := a.meteredChat(ctx, cfg, ai.ChatRequest{
		System: translationPrompt(target), Messages: []ai.Message{{Role: "user", Content: text}}, MaxTokens: 8192, Temperature: 0.2,
	}, chars)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Content), nil
}

// translateGoogleMetered 调用 Google 翻译并按字符记录用量。
func (a *App) translateGoogleMetered(ctx context.Context, caller ai.Caller, apiKey, base, text, target string, chars int64) (string, error) {
	started := time.Now()
	out, err := translateGoogle(ctx, apiKey, base, text, target)
	a.recordAIUsage(caller, "translate", "google", "google-translate", ai.Usage{}, chars, time.Since(started), err)
	return out, err
}

func httpPostJSON(ctx context.Context, url string, headers map[string]string, payload any) ([]byte, int, error) {
	body, _ := json.Marshal(payload)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return data, resp.StatusCode, nil
}

func translateGoogle(ctx context.Context, apiKey, base, text, target string) (string, error) {
	if base == "" {
		base = "https://translation.googleapis.com"
	}
	url := strings.TrimRight(base, "/") + "/language/translate/v2?key=" + apiKey
	data, status, err := httpPostJSON(ctx, url, nil, map[string]any{"q": text, "target": target, "format": "text"})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", errors.New("Google 翻译返回状态 " + http.StatusText(status))
	}
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data.Translations) == 0 {
		return "", errors.New("Google 翻译响应解析失败")
	}
	return out.Data.Translations[0].TranslatedText, nil
}

func translationPrompt(target string) string {
	return "You are a professional translator. Translate the user's text into " + target +
		". Preserve Markdown formatting, code blocks and inline code verbatim. Placeholders like ⟦12⟧ stand for formatting or code that must not be translated: " +
		"keep every placeholder exactly as written, exactly once, in the same relative position. Output only the translation without any explanation."
}
