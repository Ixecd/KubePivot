package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// LLMClient LLM 客户端接口，支持多家 provider
type LLMClient interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// NewLLMClient 根据环境变量创建对应的 LLM 客户端
//
// 环境变量：
//
//	KP_LLM_PROVIDER  = claude | openai | doubao（默认 claude）
//	KP_LLM_API_KEY   = 对应 provider 的 API key
//	KP_LLM_MODEL     = 模型名，有默认值
//	KP_LLM_ENDPOINT  = 私有化部署时指定，覆盖默认 API 地址
func NewLLMClient() (LLMClient, error) {
	provider := strings.ToLower(os.Getenv("KP_LLM_PROVIDER"))
	if provider == "" {
		provider = "claude"
	}

	apiKey := os.Getenv("KP_LLM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf(
			"KP_LLM_API_KEY 未配置，请设置环境变量\n"+
				"  export KP_LLM_API_KEY=your-api-key\n"+
				"  export KP_LLM_PROVIDER=%s  # 可选，默认 claude", provider,
		)
	}

	model := os.Getenv("KP_LLM_MODEL")
	endpoint := os.Getenv("KP_LLM_ENDPOINT")

	switch provider {
	case "claude":
		return newClaudeClient(apiKey, model, endpoint), nil
	case "deepseek":
		return newDeepSeekClient(apiKey, model, endpoint), nil
	case "openai":
		return newOpenAIClient(apiKey, model, endpoint), nil
	case "doubao":
		return newDoubaoClient(apiKey, model, endpoint), nil
	case "grok":
		return newGrokClient(apiKey, model, endpoint), nil
	default:
		return nil, fmt.Errorf("不支持的 LLM provider: %s（支持: claude / openai / doubao）", provider)
	}
}

// ── Claude ────────────────────────────────────────────────────────────────────

type claudeClient struct {
	apiKey   string
	model    string
	endpoint string
}

func newClaudeClient(apiKey, model, endpoint string) *claudeClient {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	return &claudeClient{apiKey: apiKey, model: model, endpoint: endpoint}
}

func (c *claudeClient) Complete(ctx context.Context, prompt string) (string, error) {
	body := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 2048,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 Claude API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析 Claude 响应失败: %w", err)
	}
	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("Claude 响应中没有 text block")
}

// ── OpenAI ────────────────────────────────────────────────────────────────────

type openAIClient struct {
	apiKey   string
	model    string
	endpoint string
}

func newOpenAIClient(apiKey, model, endpoint string) *openAIClient {
	if model == "" {
		model = "gpt-4o"
	}
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	return &openAIClient{apiKey: apiKey, model: model, endpoint: endpoint}
}

func newDeepSeekClient(apiKey, model, endpoint string) *openAIClient {
	if model == "" {
		model = "deepseek-chat"
	}
	if endpoint == "" {
		endpoint = "https://api.deepseek.com/v1/chat/completions"
	}
	return &openAIClient{apiKey: apiKey, model: model, endpoint: endpoint}
}

func (c *openAIClient) Complete(ctx context.Context, prompt string) (string, error) {
	body := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 2048,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 OpenAI API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析 OpenAI 响应失败: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("OpenAI 响应中没有 choices")
	}
	return result.Choices[0].Message.Content, nil
}

// ── 豆包（字节跳动）────────────────────────────────────────────────────────────

type doubaoClient struct {
	apiKey   string
	model    string
	endpoint string
}

func newDoubaoClient(apiKey, model, endpoint string) *doubaoClient {
	if model == "" {
		model = "doubao-pro-32k"
	}
	if endpoint == "" {
		endpoint = "https://ark.cn-beijing.volces.com/api/v3/chat/completions"
	}
	return &doubaoClient{apiKey: apiKey, model: model, endpoint: endpoint}
}

func (c *doubaoClient) Complete(ctx context.Context, prompt string) (string, error) {
	// 豆包 API 兼容 OpenAI 格式
	body := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 2048,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("请求豆包 API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("豆包 API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析豆包响应失败: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("豆包响应中没有 choices")
	}
	return result.Choices[0].Message.Content, nil
}

// ── Grok（xAI）────────────────────────────────────────────────────────────────

type grokClient struct {
	apiKey   string
	model    string
	endpoint string
}

func newGrokClient(apiKey, model, endpoint string) *grokClient {
	if model == "" {
		model = "grok-3"
	}
	if endpoint == "" {
		endpoint = "https://api.x.ai/v1/chat/completions"
	}
	return &grokClient{apiKey: apiKey, model: model, endpoint: endpoint}
}

func (c *grokClient) Complete(ctx context.Context, prompt string) (string, error) {
	// xAI 兼容 OpenAI 格式
	body := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 2048,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 Grok API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Grok API 返回错误 %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析 Grok 响应失败: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("Grok 响应中没有 choices")
	}
	return result.Choices[0].Message.Content, nil
}

// ── 工具 ──────────────────────────────────────────────────────────────────────

// sharedHTTPClient 包级单例连接池，避免每次请求创建新 Client 导致 TIME_WAIT 堆积。
var sharedHTTPClient = &http.Client{Timeout: 60 * time.Second}

func httpClient() *http.Client {
	return sharedHTTPClient
}
