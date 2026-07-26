package port

import "context"

// ChartGenQueue 图表 AI 异步生成的任务投递端口（出站）。
// 业务只依赖本接口；由 infra（如 RabbitMQ）实现。
type ChartGenQueue interface {
	// EnqueueGen 将 chartID 投入队列，供消费者异步执行生成。
	// 实现宜使用持久化消息，降低 broker 重启丢任务的概率。
	EnqueueGen(ctx context.Context, chartID int64) error
}
