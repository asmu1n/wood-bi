package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wood-bi/internal/infra/ai"
	"wood-bi/internal/infra/database"
	"wood-bi/internal/infra/mq/rabbit"
	"wood-bi/internal/infra/scheduler"
	"wood-bi/internal/module/chart"
	chartrepo "wood-bi/internal/module/chart/repo"
	"wood-bi/internal/pkg/logger"
)

// chart 异步生成 Worker：消费 RabbitMQ 任务并跑补偿 cron。
// 与 cmd/server 分离，便于独立扩缩 AI 并发、避免 HTTP 与长任务互相抢资源。
func main() {
	logger.Init()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- 核心依赖：任一失败直接退出 ---

	db, err := database.New()
	if err != nil {
		logger.Fatal("connect db failed", logger.FieldErr, err)
	}
	defer db.Close()

	aiClient, err := ai.New()
	if err != nil {
		logger.Fatal("ai client init failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "ai.config_fail",
			logger.FieldErr, err,
		)
	}

	mqConn, mqCfg, err := rabbit.Dial()
	if err != nil {
		logger.Fatal("rabbitmq connect failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.dial_fail",
			logger.FieldErr, err,
		)
	}
	defer mqConn.Close()

	// 补偿补投需要 Publisher；与 Consumer 共用一条 Connection。
	mqPub, err := rabbit.NewPublisher(mqConn, mqCfg)
	if err != nil {
		logger.Fatal("rabbit publisher init failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.publisher_fail",
			logger.FieldErr, err,
		)
	}
	defer mqPub.Close()

	// Worker 不走 HTTP 限流；rate limiter 传 nil。
	chartSvc := chart.NewService(chartrepo.New(db.Client), aiClient, nil, mqPub)

	mqConsumer, err := rabbit.NewConsumer(mqConn, mqCfg, chartSvc.ProcessGenJob)
	if err != nil {
		logger.Fatal("rabbit consumer init failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.consumer_fail",
			logger.FieldErr, err,
		)
	}
	if err := mqConsumer.Start(ctx); err != nil {
		logger.Fatal("rabbit consumer start failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.consumer_start_fail",
			logger.FieldErr, err,
		)
	}
	defer mqConsumer.Close()

	// 滞留 wait 补投 / 超时 running 标失败（归属 Worker，与消费同进程）。
	sched := scheduler.New()
	if _, err := sched.Schedule("0 * * * * *", func() {
		chartSvc.CompensateStaleJobs(context.Background())
	}); err != nil {
		logger.Fatal("schedule compensate job failed", logger.FieldErr, err)
	}
	sched.Start()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sched.Stop(shutdownCtx); err != nil {
			logger.Warn("scheduler stop",
				logger.FieldPurpose, logger.PurposeJob,
				logger.FieldModule, "scheduler",
				logger.FieldEvent, "cron.stop_error",
				logger.FieldErr, err,
			)
		}
	}()

	logger.Info("chart gen worker ready",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "worker.ready",
		"exchange", mqCfg.Exchange,
		"queue", mqCfg.Queue,
		"prefetch", mqCfg.Prefetch,
		"workers", mqCfg.Workers,
	)

	<-ctx.Done()
	logger.Info("chart gen worker shutting down",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "worker.shutdown",
	)
}
