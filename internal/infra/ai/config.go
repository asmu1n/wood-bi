package ai

import (
	"fmt"
	"strconv"
	"time"

	"wood-bi/internal/config"
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func loadConfig() Config {
	config.LoadEnv()
	timeoutSec, _ := strconv.Atoi(config.GetEnv("AI_TIMEOUT_SEC", "90"))
	if timeoutSec <= 0 {
		timeoutSec = 90
	}
	return Config{
		BaseURL: config.GetEnv("AI_BASE_URL", "https://api.openai.com/v1"),
		APIKey:  config.GetEnv("AI_API_KEY", ""),
		Model:   config.GetEnv("AI_MODEL", "Pro/deepseek-ai/DeepSeek-V3.2"),
		Timeout: time.Duration(timeoutSec) * time.Second,
	}
}

func (c Config) validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("AI_API_KEY is required")
	}
	if c.BaseURL == "" {
		return fmt.Errorf("AI_BASE_URL is required")
	}
	if c.Model == "" {
		return fmt.Errorf("AI_MODEL is required")
	}
	return nil
}
