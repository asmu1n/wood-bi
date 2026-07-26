package rabbit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// publisher 向 BI 队列投递 chart 生成任务，实现 port.ChartGenQueue。
type publisher struct {
	cfg  mqConfig
	conn *amqp.Connection
	mu   sync.Mutex
	ch   *amqp.Channel
}

// NewPublisher 基于已有连接创建生产者，并确保拓扑与 channel 就绪。
func NewPublisher(conn *amqp.Connection, cfg mqConfig) (*publisher, error) {
	if conn == nil {
		return nil, fmt.Errorf("rabbit: nil connection")
	}
	p := &publisher{cfg: cfg, conn: conn}

	return p, nil
}

// lazyChannel 确保 channel 存在且未关闭，如果不存在或已关闭则重新创建。
func (p *publisher) lazyChannel() error {
	// 加锁保护 channel 初始化
	p.mu.Lock()
	defer p.mu.Unlock()
	// 判断 channel 是否存在且未关闭
	if p.ch != nil && !p.ch.IsClosed() {
		return nil
	}
	// 创建新的 channel
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("rabbit: open publish channel: %w", err)
	}
	if err := declareTopology(ch, p.cfg); err != nil {
		ch.Close()
		return fmt.Errorf("rabbit: declare topology: %w", err)
	}
	p.ch = ch
	return nil
}

// genMessage 队列消息体（JSON）。
type genMessage struct {
	ChartID int64 `json:"chartId"`
}

// EnqueueGen 发布持久化任务消息：body 为 {"chartId":...}。
func (p *publisher) EnqueueGen(ctx context.Context, chartID int64) error {
	if chartID <= 0 {
		return fmt.Errorf("rabbit: invalid chartID")
	}
	// 检查 channel 是否存在且未关闭，如果不存在或已关闭则重新创建
	if err := p.lazyChannel(); err != nil {
		return err
	}

	// 序列化消息体
	body, err := json.Marshal(genMessage{ChartID: chartID})
	if err != nil {
		return err
	}

	// 在锁的保护下拷贝指针，避免在锁里做 io 操作
	p.mu.Lock()
	defer p.mu.Unlock()
	// 发布消息
	return p.ch.PublishWithContext(ctx,
		p.cfg.Exchange,
		p.cfg.RoutingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}

// Close 关闭生产者 channel，不关闭共享 Connection。
func (p *publisher) Close() error {
	// 在锁的保护下关闭 channel
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		err := p.ch.Close()
		p.ch = nil
		return err
	}
	return nil
}
