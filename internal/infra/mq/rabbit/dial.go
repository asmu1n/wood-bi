package rabbit

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Dial 建立到 RabbitMQ 的连接，并返回加载后的 Config。
// 失败时由 cmd 做 Fatal；Connection 生命周期由调用方 Close。
func Dial() (*amqp.Connection, mqConfig, error) {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		return nil, cfg, err
	}
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, cfg, fmt.Errorf("rabbit dial: %w", err)
	}
	return conn, cfg, nil
}
