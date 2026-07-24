package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"wood-bi/internal/httpapi"
	"wood-bi/internal/infra/database"
	"wood-bi/internal/infra/redis"
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
	_ = redisClient // Phase 2+：cache / lock / rate limit

	userSvc := user.NewService(userrepo.New(db.Client))

	r.Use(sessions.Sessions("session", store))

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	httpapi.RegisterRouter(r, userSvc)

	logger.Info("http server starting",
		logger.FieldPurpose, logger.PurposeInfra,
		logger.FieldEvent, "http.listen",
		"addr", ":8080",
	)
	if err := r.Run(":8080"); err != nil {
		logger.Fatal("http server stopped", logger.FieldErr, err)
	}
}
