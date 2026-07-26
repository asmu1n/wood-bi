package rabbit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"wood-bi/internal/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

// GenJobFunc 单条 chart 生成任务的业务回调（通常为 chart.Service.ProcessGenJob）。
// 返回 nil：处理结束（成功或业务失败已落库）→ ack。
// 返回 error：瞬时故障 → nack 并 requeue。
type GenJobFunc func(ctx context.Context, chartID int64) error

// Consumer 从 BI 队列拉取任务并调用 GenJobFunc；与 HTTP 同进程或独立 worker 均可挂载。
type Consumer struct {
	cfg    mqConfig
	conn   *amqp.Connection
	handle GenJobFunc
	log    *slog.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	ch     *amqp.Channel
}

// NewConsumer 创建消费者；须再调用 Start 才会真正拉取消息。
func NewConsumer(conn *amqp.Connection, cfg mqConfig, handle GenJobFunc) (*Consumer, error) {
	if conn == nil {
		return nil, fmt.Errorf("rabbit: nil connection")
	}
	if handle == nil {
		return nil, fmt.Errorf("rabbit: nil job handler")
	}
	return &Consumer{
		cfg:    cfg,
		conn:   conn,
		handle: handle,
		log:    logger.Module("rabbit"),
	}, nil
}

// Start 在后台启动消费循环，直到 ctx 取消或 Close。
func (c *Consumer) Start(ctx context.Context) error {
	// 创建带取消功能的上下文
	runCtx, cancel := context.WithCancel(ctx)
	// 互斥锁完成 `cancel` 的初始化
	c.mu.Lock()
	// 只允许启动一次
	if c.cancel != nil {
		c.mu.Unlock()
		cancel()
		return fmt.Errorf("rabbit: consumer already started")
	}
	c.cancel = cancel
	c.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.loop(runCtx)
	}()
	return nil
}

// Close 停止消费循环并关闭当前 channel。
func (c *Consumer) Close() error {
	// 互斥锁更新状态
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	ch := c.ch
	c.ch = nil
	c.mu.Unlock()

	c.wg.Wait()
	if ch != nil {
		return ch.Close()
	}
	return nil
}

// loop 消费会话循环：断线或 channel 异常时退避重连。
func (c *Consumer) loop(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		// 尝试消费消息，如果报错则记录日志并重试（`consumeOnce` 会话级长时间阻塞接收消息）
		if err := c.consumeOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Error("consumer session ended",
				logger.FieldPurpose, logger.PurposeInfra,
				logger.FieldEvent, "rabbit.consume_error",
				logger.FieldErr, err,
			)
			// 判断 ctx 是否取消，如果取消则直接退出 ，如果没有则等待 backoff 时间后重试
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
}

// consumeOnce 打开 channel、声明拓扑、Consume，并阻塞处理投递直至 ctx 结束或 channel 关闭。
func (c *Consumer) consumeOnce(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer ch.Close()

	if err := declareTopology(ch, c.cfg); err != nil {
		return fmt.Errorf("declare topology: %w", err)
	}
	if err := ch.Qos(c.cfg.Prefetch, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}

	// 在互斥锁的保护下，设置当前 channel，并且完成消费后清除
	c.mu.Lock()
	c.ch = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.ch == ch {
			c.ch = nil
		}
		c.mu.Unlock()
	}()

	// 开始消费
	deliveries, err := ch.Consume(
		c.cfg.Queue,
		"",    // consumer tag
		false, // auto-ack：手动确认
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	c.log.Info("consumer started",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "rabbit.consume_start",
		"queue", c.cfg.Queue,
		"prefetch", c.cfg.Prefetch,
	)

	// 循环从消息队列里接收消息，直到 ctx 结束或 channel 关闭
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("delivery channel closed")
			}
			c.handleDelivery(ctx, &d)
		}
	}
}

// handleDelivery 解析消息、执行业务回调，并按结果 ack / nack。
func (c *Consumer) handleDelivery(ctx context.Context, d *amqp.Delivery) {
	chartID, err := parseChartID(d.Body)
	if err != nil {
		c.log.Error("invalid message body",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.bad_message",
			logger.FieldErr, err,
			"body", string(d.Body),
		)
		d.Nack(false, false) // 非法消息丢弃，避免死循环
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// 执行业务回调
	if err := c.handle(jobCtx, chartID); err != nil {
		c.log.Error("job failed, requeue",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "rabbit.job_requeue",
			logger.FieldErr, err,
			"chartId", chartID,
		)
		d.Nack(false, true) // 瞬时错误：重新入队
		return
	}
	if err := d.Ack(false); err != nil {
		c.log.Error("ack failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.ack_error",
			logger.FieldErr, err,
			"chartId", chartID,
		)
	}
}

// parseChartID 从消息体解析 chartID，支持 JSON {"chartId":n} 或纯数字字符串。
func parseChartID(body []byte) (int64, error) {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return 0, fmt.Errorf("empty body")
	}
	var msg struct {
		ChartID int64 `json:"chartId"`
	}
	if err := json.Unmarshal(body, &msg); err == nil && msg.ChartID > 0 {
		return msg.ChartID, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("cannot parse chartId from %q", s)
	}
	return id, nil
}
