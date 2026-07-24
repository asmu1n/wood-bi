package httpapi

import (
	"wood-bi/internal/module/chart"
	charthttp "wood-bi/internal/module/chart/http"
	"wood-bi/internal/module/user"
	userhttp "wood-bi/internal/module/user/http"

	"github.com/gin-gonic/gin"
)

// RegisterRouter 注册全部 HTTP 路由。
func RegisterRouter(r *gin.Engine, userSvc *user.Service, chartSvc *chart.Service) {
	registerHealth(r)

	api := r.Group("/api")

	userhttp.Register(api, userSvc)
	charthttp.Register(api, chartSvc)
}
