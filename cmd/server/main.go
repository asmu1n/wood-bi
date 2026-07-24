package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"wood-bi/internal/httpapi"
	"wood-bi/internal/infra/ai"
	"wood-bi/internal/infra/database"
	"wood-bi/internal/infra/ratelimit"
	"wood-bi/internal/infra/redis"
	"wood-bi/internal/module/chart"
	chartrepo "wood-bi/internal/module/chart/repo"
	"wood-bi/internal/module/user"
	userrepo "wood-bi/internal/module/user/repo"
	"wood-bi/internal/pkg/logger"
	"wood-bi/internal/port"

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

	userSvc := user.NewService(userrepo.New(db.Client))

	var aiClient port.AI
	aiImpl, err := ai.New()
	if err != nil {
		// 允许无 AI Key 启动（CRUD 可用）；/chart/gen 会返回业务错误
		logger.Warn("ai client not configured",
			logger.FieldPurpose, logger.PurposeInfra,
			logger.FieldEvent, "ai.config_skip",
			logger.FieldErr, err,
		)
	} else {
		aiClient = aiImpl
	}

	limiter := ratelimit.New(redisClient)
	chartSvc := chart.NewService(chartrepo.New(db.Client), aiClient, limiter)

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
