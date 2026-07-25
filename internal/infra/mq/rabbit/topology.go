package rabbit

import amqp "github.com/rabbitmq/amqp091-go"

// declareTopology 声明 BI 任务用的 durable direct 交换机、队列，并完成 bind。
// 生产/消费启动前幂等调用，保证拓扑存在。
func declareTopology(ch *amqp.Channel, cfg mqConfig) error {
	if err := ch.ExchangeDeclare(
		cfg.Exchange,
		"direct",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(
		cfg.Queue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	); err != nil {
		return err
	}
	return ch.QueueBind(cfg.Queue, cfg.RoutingKey, cfg.Exchange, false, nil)
}
