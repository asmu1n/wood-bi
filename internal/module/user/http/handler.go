package userhttp

import (
	"wood-bi/internal/httpapi/middleware"
	"wood-bi/internal/module/user"
	"wood-bi/internal/pkg/response"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// Handler 用户 HTTP 传输层。
type Handler struct {
	svc *user.Service
}

func NewHandler(svc *user.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 挂载 /user 路由组。
func Register(rg *gin.RouterGroup, svc *user.Service) {
	h := NewHandler(svc)
	g := rg.Group("/user")

	g.POST("/register", h.Register)
	g.POST("/login", h.Login)

	auth := g.Group("")
	auth.Use(middleware.AuthRequired())
	{
		auth.POST("/logout", h.Logout)
		auth.GET("/current", h.Current)
		auth.POST("/update/my", h.UpdateMy)
	}

	admin := g.Group("")
	admin.Use(middleware.AuthRequired(), middleware.AdminRequired())
	{
		admin.POST("/add", h.AdminCreate)
		admin.POST("/delete", h.AdminDelete)
		admin.POST("/update", h.AdminUpdate)
		admin.GET("/get", h.AdminGet)
		admin.POST("/list/page", h.AdminListPage)
	}
}

// Register godoc
// @Summary      用户注册
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        body body user.RegisterInput true "注册信息"
// @Success      200  {object}  response.Response
// @Router       /user/register [post]
func (h *Handler) Register(c *gin.Context) {
	var in user.RegisterInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	id, err := h.svc.Register(c.Request.Context(), in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, id)
}

// Login godoc
// @Summary      用户登录
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        body body user.LoginInput true "登录信息"
// @Success      200  {object}  response.Response
// @Router       /user/login [post]
func (h *Handler) Login(c *gin.Context) {
	var in user.LoginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	u, err := h.svc.Login(c.Request.Context(), in)
	if err != nil {
		response.RespondError(c, err)
		return
	}

	sess := sessions.Default(c)
	sess.Set(middleware.SessionKeyUserID, u.ID)
	sess.Set(middleware.SessionKeyUserRole, u.Role)
	if err := sess.Save(); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, u)
}

// Logout godoc
// @Summary      用户注销
// @Tags         user
// @Produce      json
// @Success      200  {object}  response.Response
// @Router       /user/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Clear()
	if err := sess.Save(); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, true)
}

// Current godoc
// @Summary      当前登录用户
// @Tags         user
// @Produce      json
// @Success      200  {object}  response.Response
// @Router       /user/current [get]
func (h *Handler) Current(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	u, err := h.svc.GetByID(c.Request.Context(), uid)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, u)
}

// UpdateMy godoc
// @Summary      更新当前用户资料
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        body body user.UpdateMyInput true "资料"
// @Success      200  {object}  response.Response
// @Router       /user/update/my [post]
func (h *Handler) UpdateMy(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	var in user.UpdateMyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	u, err := h.svc.UpdateMy(c.Request.Context(), uid, in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, u)
}

// AdminCreate godoc
// @Summary      管理员创建用户
// @Tags         user-admin
// @Accept       json
// @Produce      json
// @Param        body body user.AdminCreateInput true "用户"
// @Success      200  {object}  response.Response
// @Router       /user/add [post]
func (h *Handler) AdminCreate(c *gin.Context) {
	var in user.AdminCreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	id, err := h.svc.AdminCreate(c.Request.Context(), in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, id)
}

// AdminDelete godoc
// @Summary      管理员删除用户
// @Tags         user-admin
// @Accept       json
// @Produce      json
// @Param        body body map[string]int64 true "id"
// @Success      200  {object}  response.Response
// @Router       /user/delete [post]
func (h *Handler) AdminDelete(c *gin.Context) {
	var body struct {
		ID int64 `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	if err := h.svc.AdminDelete(c.Request.Context(), body.ID); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, true)
}

// AdminUpdate godoc
// @Summary      管理员更新用户
// @Tags         user-admin
// @Accept       json
// @Produce      json
// @Param        body body user.AdminUpdateInput true "用户"
// @Success      200  {object}  response.Response
// @Router       /user/update [post]
func (h *Handler) AdminUpdate(c *gin.Context) {
	var in user.AdminUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	u, err := h.svc.AdminUpdate(c.Request.Context(), in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, u)
}

// AdminGet godoc
// @Summary      管理员按 ID 获取用户
// @Tags         user-admin
// @Produce      json
// @Param        id query int64 true "用户 ID"
// @Success      200  {object}  response.Response
// @Router       /user/get [get]
func (h *Handler) AdminGet(c *gin.Context) {
	var q struct {
		ID int64 `form:"id" binding:"required"`
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	u, err := h.svc.GetByID(c.Request.Context(), q.ID)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, u)
}

// AdminListPage godoc
// @Summary      管理员分页查询用户
// @Tags         user-admin
// @Accept       json
// @Produce      json
// @Param        body body user.QueryParams true "查询"
// @Success      200  {object}  response.Response
// @Router       /user/list/page [post]
func (h *Handler) AdminListPage(c *gin.Context) {
	var q user.QueryParams
	if err := c.ShouldBindJSON(&q); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	page, err := h.svc.ListPage(c.Request.Context(), q)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, page)
}
