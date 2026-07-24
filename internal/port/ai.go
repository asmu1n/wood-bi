package port

import "context"

// ChatMessage OpenAI 兼容的对话消息。
type ChatMessage struct {
	Role    string // system / user / assistant
	Content string
}

// ChatRequest 一次补全请求。
type ChatRequest struct {
	Messages    []ChatMessage
	// JSONMode 为 true 时尽量要求模型输出 JSON（供应商支持则开启 response_format）。
	JSONMode bool
	// Model 可选覆盖默认模型；空则用客户端配置。
	Model string
}

// ChatResponse 模型返回的文本内容。
type ChatResponse struct {
	Content string
}

// AI OpenAI 兼容 Chat Completions 端口。
type AI interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
