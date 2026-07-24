package chart

import "context"

// Repository 图表持久化端口。
type Repository interface {
	Create(ctx context.Context, c *Chart) (*Chart, error)
	GetByID(ctx context.Context, id int64) (*Chart, error)
	Update(ctx context.Context, id int64, mut UpdateMutation) (*Chart, error)
	SoftDelete(ctx context.Context, id int64) error
	ListPage(ctx context.Context, q QueryParams) ([]*Chart, int64, error)
}

// UpdateMutation 部分字段更新；指针 nil 表示不改。
type UpdateMutation struct {
	Name        *string
	Goal        *string
	ChartData   *string
	ChartType   *string
	GenChart    *string
	GenResult   *string
	Status      *string
	ExecMessage *string
}
