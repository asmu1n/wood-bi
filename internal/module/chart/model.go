package chart

import (
	"time"

	"wood-bi/internal/pkg/page"
)

// 任务状态。
const (
	StatusWait    = "wait"
	StatusRunning = "running"
	StatusSucceed = "succeed"
	StatusFailed  = "failed"
)

// Chart 图表领域模型。
type Chart struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name,omitempty"`
	Goal        string    `json:"goal,omitempty"`
	ChartData   string    `json:"chartData,omitempty"`
	ChartType   string    `json:"chartType,omitempty"`
	GenChart    string    `json:"genChart,omitempty"`
	GenResult   string    `json:"genResult,omitempty"`
	Status      string    `json:"status"`
	ExecMessage string    `json:"execMessage,omitempty"`
	UserID      int64     `json:"userId"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CreateInput 手动创建图表（不含 AI）。
type CreateInput struct {
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	ChartData string `json:"chartData"`
	ChartType string `json:"chartType"`
}

// EditInput 用户编辑。
type EditInput struct {
	ID        int64  `json:"id" binding:"required"`
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	ChartData string `json:"chartData"`
	ChartType string `json:"chartType"`
}

// AdminUpdateInput 管理员更新（可改生成结果与状态）。
type AdminUpdateInput struct {
	ID          int64   `json:"id" binding:"required"`
	Name        *string `json:"name"`
	Goal        *string `json:"goal"`
	ChartData   *string `json:"chartData"`
	ChartType   *string `json:"chartType"`
	GenChart    *string `json:"genChart"`
	GenResult   *string `json:"genResult"`
	Status      *string `json:"status"`
	ExecMessage *string `json:"execMessage"`
}

// QueryParams 分页查询。
type QueryParams struct {
	page.PageRequest
	ID        int64  `json:"id" form:"id"`
	Name      string `json:"name" form:"name"`
	Goal      string `json:"goal" form:"goal"`
	ChartType string `json:"chartType" form:"chartType"`
	UserID    int64  `json:"userId" form:"userId"`
	Status    string `json:"status" form:"status"`
}

// GenInput AI 生成请求（multipart 表单字段）。
type GenInput struct {
	Name      string
	Goal      string
	ChartType string
	// FileBytes 已读入的 Excel 内容。
	FileBytes []byte
	// Filename 用于后缀校验。
	Filename string
}

// GenResult 同步生成成功返回。
type GenResult struct {
	ChartID   int64  `json:"chartId"`
	GenChart  string `json:"genChart"`
	GenResult string `json:"genResult"`
}

// AIOutput 模型应返回的 JSON 结构。
type AIOutput struct {
	Option     map[string]any `json:"option"`
	Conclusion string         `json:"conclusion"`
}
