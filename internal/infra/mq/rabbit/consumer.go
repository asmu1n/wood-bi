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

// Consumer 从 BI 队列拉取任务并调用 GenJobFunc。
// 采用多 Channel 竞争消费：每个 worker 独立 AMQP Channel，串行 handle + ack。
// 可与 HTTP 同进程挂载，也可拆到独立进程。
type Consumer struct {
	cfg         mqConfig
	conn        *amqp.Connection
	handle      GenJobFunc
	log         *slog.Logger
	workerCount int

	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

// NewConsumer 创建消费者；须再调用 Start 才会真正拉取消息。
// worker 数取 cfg.Workers（<=0 时回落为 1）。
func NewConsumer(conn *amqp.Connection, cfg mqConfig, handle GenJobFunc) (*Consumer, error) {
	if conn == nil {
		return nil, fmt.Errorf("rabbit: nil connection")
	}
	if handle == nil {
		return nil, fmt.Errorf("rabbit: nil job handler")
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}

	return &Consumer{
		cfg:         cfg,
		conn:        conn,
		handle:      handle,
		log:         logger.Module("rabbit"),
		workerCount: workers,
	}, nil
}

// Start 启动 workerCount 条后台消费循环，直到 ctx 取消或 Close。
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return fmt.Errorf("rabbit: consumer already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()

	for id := 0; id < c.workerCount; id++ {
		workerID := id
		c.wg.Add(1)
		go c.loop(runCtx, workerID)
	}

	c.log.Info("consumer workers started",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "rabbit.consume_workers_start",
		"workers", c.workerCount,
		"prefetch", c.cfg.Prefetch,
		"queue", c.cfg.Queue,
	)
	return nil
}

// Close 取消所有 worker 并等待其退出（含 in-flight handle 随 ctx 取消返回）。
func (c *Consumer) Close() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
	return nil
}

// loop 单 worker 会话循环：channel/投递异常时退避重连。
func (c *Consumer) loop(ctx context.Context, workerID int) {
	defer c.wg.Done()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		started := time.Now()
		err := c.consumeOnce(ctx, workerID)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			// 正常停（ctx 取消）
			return
		}

		// 会话曾稳定运行过一段时间，则重置退避，避免偶发闪断后仍长时间空等
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}

		c.log.Error("consumer session ended",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.consume_error",
			logger.FieldErr, err,
			"workerId", workerID,
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// consumeOnce 打开独立 channel、声明拓扑、Consume，并串行处理投递。
func (c *Consumer) consumeOnce(ctx context.Context, workerID int) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("worker %d open channel: %w", workerID, err)
	}
	defer ch.Close()

	if err := declareTopology(ch, c.cfg); err != nil {
		return fmt.Errorf("worker %d declare topology: %w", workerID, err)
	}
	// 每 channel 独立 QoS；总未确认约 workers×prefetch
	if err := ch.Qos(c.cfg.Prefetch, 0, false); err != nil {
		return fmt.Errorf("worker %d qos: %w", workerID, err)
	}

	// 空 tag 由 broker 生成，避免多实例/重连冲突；本地仅用 workerID 打日志
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
		return fmt.Errorf("worker %d consume: %w", workerID, err)
	}

	c.log.Info("consumer session started",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "rabbit.consume_start",
		"workerId", workerID,
		"queue", c.cfg.Queue,
		"prefetch", c.cfg.Prefetch,
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("worker %d delivery channel closed", workerID)
			}
			c.handleDelivery(ctx, workerID, &d)
		}
	}
}

// handleDelivery 解析消息、执行业务回调，并按结果 ack / nack。
func (c *Consumer) handleDelivery(ctx context.Context, workerID int, d *amqp.Delivery) {
	chartID, err := parseChartID(d.Body)
	if err != nil {
		c.log.Error("invalid message body",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.bad_message",
			logger.FieldErr, err,
			"workerId", workerID,
			"body", string(d.Body),
		)
		_ = d.Nack(false, false) // 非法消息丢弃，避免死循环
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	if err := c.handle(jobCtx, chartID); err != nil {
		// 关停取消：不刷 error 噪音，仍 requeue 让其它实例/下次再跑
		if ctx.Err() != nil {
			_ = d.Nack(false, true)
			return
		}
		c.log.Error("job failed, requeue",
			logger.FieldPurpose, logger.PurposeBiz,
			logger.FieldEvent, "rabbit.job_requeue",
			logger.FieldErr, err,
			"workerId", workerID,
			"chartId", chartID,
		)
		_ = d.Nack(false, true)
		return
	}

	if err := d.Ack(false); err != nil {
		c.log.Error("ack failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.ack_error",
			logger.FieldErr, err,
			"workerId", workerID,
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
