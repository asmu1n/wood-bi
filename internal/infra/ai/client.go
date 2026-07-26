package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"wood-bi/internal/port"
)

// client OpenAI 兼容 Chat Completions 实现。
type client struct {
	cfg        Config
	httpClient *http.Client
}

// New 从环境变量创建 AI 客户端；API Key 未配置时返回错误。
func New() (*client, error) {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return NewWithConfig(cfg), nil
}

// NewWithConfig 使用显式配置创建客户端（便于测试）。
func NewWithConfig(cfg Config) *client {
	return &client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

type openAIRequest struct {
	Model          string            `json:"model"`
	Messages       []openAIMessage   `json:"messages"`
	ResponseFormat *openAIRespFormat `json:"response_format,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRespFormat struct {
	Type string `json:"type"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat 调用 /chat/completions。
func (c *client) Chat(ctx context.Context, req port.ChatRequest) (*port.ChatResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("ai: empty messages")
	}

	model := req.Model
	if model == "" {
		model = c.cfg.Model
	}

	body := openAIRequest{
		Model:    model,
		Messages: make([]openAIMessage, 0, len(req.Messages)),
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, openAIMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	if req.JSONMode {
		body.ResponseFormat = &openAIRespFormat{Type: "json_object"}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ai: marshal request: %w", err)
	}

	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ai: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ai: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ai: read body: %w", err)
	}

	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ai: decode response (status %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("ai: api error: %s", parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ai: unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("ai: empty choices")
	}

	return &port.ChatResponse{Content: parsed.Choices[0].Message.Content}, nil
}
