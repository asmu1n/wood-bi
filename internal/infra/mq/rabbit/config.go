package rabbit

import (
	"fmt"
	"strconv"

	"wood-bi/internal/config"
)

// mqConfig RabbitMQ 连接与 BI 任务拓扑配置。
type mqConfig struct {
	URL        string // AMQP 地址，如 amqp://guest:guest@localhost:5672/
	Exchange   string // 交换机名（direct）
	Queue      string // 队列名
	RoutingKey string // 绑定路由键
	Prefetch   int    // 消费端 QoS：未确认消息上限，限制并发
}

// LoadConfig 从环境变量加载配置（公开给装配层复用）。
func LoadConfig() mqConfig {
	return loadConfig()
}

// loadConfig 读取 RABBITMQ_* 环境变量并填充默认拓扑名。
func loadConfig() mqConfig {
	config.LoadEnv()
	prefetch, _ := strconv.Atoi(config.GetEnv("RABBITMQ_PREFETCH", "2"))
	if prefetch <= 0 {
		prefetch = 2
	}
	return mqConfig{
		URL:        config.GetEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange:   config.GetEnv("RABBITMQ_BI_EXCHANGE", "bi_exchange"),
		Queue:      config.GetEnv("RABBITMQ_BI_QUEUE", "bi_queue"),
		RoutingKey: config.GetEnv("RABBITMQ_BI_ROUTING_KEY", "bi_routingKey"),
		Prefetch:   prefetch,
	}
}

// validate 校验连接串与拓扑名称非空。
func (c mqConfig) validate() error {
	if c.URL == "" {
		return fmt.Errorf("RABBITMQ_URL is empty")
	}
	if c.Exchange == "" || c.Queue == "" || c.RoutingKey == "" {
		return fmt.Errorf("rabbitmq topology names must not be empty")
	}
	return nil
}
