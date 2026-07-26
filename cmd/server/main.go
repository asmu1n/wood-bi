package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wood-bi/internal/httpapi"
	"wood-bi/internal/infra/ai"
	"wood-bi/internal/infra/database"
	"wood-bi/internal/infra/mq/rabbit"
	"wood-bi/internal/infra/ratelimit"
	"wood-bi/internal/infra/redis"
	"wood-bi/internal/infra/scheduler"
	"wood-bi/internal/module/chart"
	chartrepo "wood-bi/internal/module/chart/repo"
	"wood-bi/internal/module/user"
	userrepo "wood-bi/internal/module/user/repo"
	"wood-bi/internal/pkg/logger"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "wood-bi/docs/api/swagger"
)

// @title           wood-bi API
// @version         1.0
// @description     AI BI 后端（Go）：用户与图表分析
// @host            localhost:8080
// @BasePath        /api
// @securityDefinitions.apikey SessionAuth
// @in header
// @name Cookie
// @description Session cookie authentication. Example: session=your-session-id

func main() {
	logger.Init()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := gin.Default()

	// --- 核心依赖：任一失败直接退出 ---

	db, err := database.New()
	if err != nil {
		logger.Fatal("connect db failed", logger.FieldErr, err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		logger.Fatal("migrate failed", logger.FieldErr, err)
	}

	store, err := redis.NewSessionStore()
	if err != nil {
		logger.Fatal("load redis session store failed", logger.FieldErr, err)
	}

	redisClient, err := redis.NewClient()
	if err != nil {
		logger.Fatal("connect redis failed", logger.FieldErr, err)
	}
	defer redisClient.Close()

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

	mqPub, err := rabbit.NewPublisher(mqConn, mqCfg)
	if err != nil {
		logger.Fatal("rabbit publisher init failed",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "rabbit.publisher_fail",
			logger.FieldErr, err,
		)
	}
	defer mqPub.Close()

	limiter := ratelimit.New(redisClient)
	userSvc := user.NewService(userrepo.New(db.Client))
	chartSvc := chart.NewService(chartrepo.New(db.Client), aiClient, limiter, mqPub)

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

	logger.Info("rabbitmq ready",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "rabbit.ready",
		"exchange", mqCfg.Exchange,
		"queue", mqCfg.Queue,
		"prefetch", mqCfg.Prefetch,
		"workers", mqCfg.Workers,
	)

	// 定时补偿：滞留 wait 补投 / 超时 running 标失败
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

	r.Use(sessions.Sessions("session", store))
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	httpapi.RegisterRouter(r, userSvc, chartSvc)

	logger.Info("http server starting",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "http.listen",
		"addr", ":8080",
	)
	if err := r.Run(":8080"); err != nil {
		logger.Fatal("http server stopped", logger.FieldErr, err)
	}
}
