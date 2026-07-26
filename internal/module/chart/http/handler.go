package charthttp

import (
	"io"
	"path/filepath"

	"wood-bi/internal/httpapi/middleware"
	"wood-bi/internal/module/chart"
	"wood-bi/internal/module/user"
	"wood-bi/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler 图表 HTTP 层。
type Handler struct {
	svc *chart.Service
}

func NewHandler(svc *chart.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 挂载 /chart 路由。
func Register(rg *gin.RouterGroup, svc *chart.Service) {
	h := NewHandler(svc)
	g := rg.Group("/chart")
	g.Use(middleware.AuthRequired())
	{
		g.POST("/add", h.Add)
		g.POST("/delete", h.Delete)
		g.POST("/edit", h.Edit)
		g.GET("/get", h.Get)
		g.POST("/list/page", h.ListPage)
		g.POST("/my/list/page", h.ListMyPage)
		g.POST("/gen", h.Gen)
		g.POST("/gen/async", h.GenAsync)
		g.POST("/update", middleware.AdminRequired(), h.AdminUpdate)
	}
}

func isAdmin(c *gin.Context) bool {
	role, err := middleware.GetLoginUserRole(c)
	if err != nil {
		return false
	}
	return user.IsAdmin(role)
}

// Add 创建图表。
func (h *Handler) Add(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	var in chart.CreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	id, err := h.svc.Create(c.Request.Context(), uid, in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, id)
}

// Delete 删除图表。
func (h *Handler) Delete(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	var body struct {
		ID int64 `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), uid, isAdmin(c), body.ID); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, true)
}

// Edit 用户编辑。
func (h *Handler) Edit(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	var in chart.EditInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	if err := h.svc.Edit(c.Request.Context(), uid, isAdmin(c), in); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, true)
}

// AdminUpdate 管理员更新。
func (h *Handler) AdminUpdate(c *gin.Context) {
	var in chart.AdminUpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	if err := h.svc.AdminUpdate(c.Request.Context(), in); err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, true)
}

// Get 按 ID 查询。
func (h *Handler) Get(c *gin.Context) {
	var q struct {
		ID int64 `form:"id" binding:"required"`
	}
	if err := c.ShouldBindQuery(&q); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	row, err := h.svc.GetByID(c.Request.Context(), q.ID)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, row)
}

// ListPage 分页列表。
func (h *Handler) ListPage(c *gin.Context) {
	var q chart.QueryParams
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

// ListMyPage 我的图表分页。
func (h *Handler) ListMyPage(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	var q chart.QueryParams
	if err := c.ShouldBindJSON(&q); err != nil {
		response.RespondBindingError(c, err)
		return
	}
	page, err := h.svc.ListMyPage(c.Request.Context(), uid, q)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, page)
}

// Gen 同步 AI 生成。
// multipart: file + name + goal + chartType
func (h *Handler) Gen(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	in, err := bindGenMultipart(c)
	if err != nil {
		response.RespondBindingError(c, err)
		return
	}
	result, err := h.svc.GenerateSync(c.Request.Context(), uid, in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, result)
}

// GenAsync 异步 AI 生成：multipart 上传后投递 MQ，响应仅含 chartId。
func (h *Handler) GenAsync(c *gin.Context) {
	uid, err := middleware.GetLoginUserID(c)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	in, err := bindGenMultipart(c)
	if err != nil {
		response.RespondBindingError(c, err)
		return
	}
	result, err := h.svc.GenerateAsync(c.Request.Context(), uid, in)
	if err != nil {
		response.RespondError(c, err)
		return
	}
	response.RespondOK(c, result)
}

func bindGenMultipart(c *gin.Context) (chart.GenInput, error) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return chart.GenInput{}, err
	}
	f, err := fileHeader.Open()
	if err != nil {
		return chart.GenInput{}, err
	}
	defer f.Close()

	const maxRead = 1<<20 + 1024
	data, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil {
		return chart.GenInput{}, err
	}
	return chart.GenInput{
		Name:      c.PostForm("name"),
		Goal:      c.PostForm("goal"),
		ChartType: c.PostForm("chartType"),
		FileBytes: data,
		Filename:  filepath.Base(fileHeader.Filename),
	}, nil
}
