package httpapi

import (
	"wood-bi/internal/module/user"
	userhttp "wood-bi/internal/module/user/http"

	"github.com/gin-gonic/gin"
)

// RegisterRouter 注册全部 HTTP 路由。
// 新增业务模块时：在此挂载路由，并在 cmd/server 中装配 Service 后传入。
func RegisterRouter(r *gin.Engine, userSvc *user.Service) {
	registerHealth(r)

	api := r.Group("/api")
	userhttp.Register(api, userSvc)
}
