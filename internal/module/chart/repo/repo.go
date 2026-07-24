package repo

import (
	"context"
	"time"

	"wood-bi/ent"
	entchart "wood-bi/ent/chart"
	"wood-bi/internal/module/chart"

	"entgo.io/ent/dialect/sql"
)

// Repo 基于 Ent 的图表仓储。
type Repo struct {
	client *ent.Client
}

func New(client *ent.Client) *Repo {
	return &Repo{client: client}
}

func (r *Repo) Create(ctx context.Context, c *chart.Chart) (*chart.Chart, error) {
	status := c.Status
	if status == "" {
		status = chart.StatusWait
	}
	row, err := r.client.Chart.Create().
		SetNillableName(strPtr(c.Name)).
		SetNillableGoal(strPtr(c.Goal)).
		SetNillableChartData(strPtr(c.ChartData)).
		SetNillableChartType(strPtr(c.ChartType)).
		SetNillableGenChart(strPtr(c.GenChart)).
		SetNillableGenResult(strPtr(c.GenResult)).
		SetStatus(status).
		SetNillableExecMessage(strPtr(c.ExecMessage)).
		SetUserID(c.UserID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toDomain(row), nil
}

func (r *Repo) GetByID(ctx context.Context, id int64) (*chart.Chart, error) {
	row, err := r.client.Chart.Query().
		Where(
			entchart.IDEQ(id),
			entchart.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return toDomain(row), nil
}

func (r *Repo) Update(ctx context.Context, id int64, mut chart.UpdateMutation) (*chart.Chart, error) {
	upd := r.client.Chart.UpdateOneID(id).Where(entchart.DeletedAtIsNil())
	if mut.Name != nil {
		upd.SetName(*mut.Name)
	}
	if mut.Goal != nil {
		upd.SetGoal(*mut.Goal)
	}
	if mut.ChartData != nil {
		upd.SetChartData(*mut.ChartData)
	}
	if mut.ChartType != nil {
		upd.SetChartType(*mut.ChartType)
	}
	if mut.GenChart != nil {
		upd.SetGenChart(*mut.GenChart)
	}
	if mut.GenResult != nil {
		upd.SetGenResult(*mut.GenResult)
	}
	if mut.Status != nil {
		upd.SetStatus(*mut.Status)
	}
	if mut.ExecMessage != nil {
		upd.SetExecMessage(*mut.ExecMessage)
	}
	row, err := upd.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return toDomain(row), nil
}

func (r *Repo) SoftDelete(ctx context.Context, id int64) error {
	now := time.Now()
	_, err := r.client.Chart.Update().
		Where(
			entchart.IDEQ(id),
			entchart.DeletedAtIsNil(),
		).
		SetDeletedAt(now).
		Save(ctx)
	return err
}

func (r *Repo) ListPage(ctx context.Context, q chart.QueryParams) ([]*chart.Chart, int64, error) {
	query := r.client.Chart.Query().Where(entchart.DeletedAtIsNil())
	if q.ID > 0 {
		query = query.Where(entchart.IDEQ(q.ID))
	}
	if q.Name != "" {
		query = query.Where(entchart.NameContains(q.Name))
	}
	if q.Goal != "" {
		query = query.Where(entchart.GoalContains(q.Goal))
	}
	if q.ChartType != "" {
		query = query.Where(entchart.ChartTypeEQ(q.ChartType))
	}
	if q.UserID > 0 {
		query = query.Where(entchart.UserIDEQ(q.UserID))
	}
	if q.Status != "" {
		query = query.Where(entchart.StatusEQ(q.Status))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	rows, err := query.
		Order(entchart.ByCreatedAt(sql.OrderDesc())).
		Offset(q.Offset()).
		Limit(q.Limit()).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	out := make([]*chart.Chart, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, int64(total), nil
}

func toDomain(row *ent.Chart) *chart.Chart {
	if row == nil {
		return nil
	}
	c := &chart.Chart{
		ID:        row.ID,
		Status:    row.Status,
		UserID:    row.UserID,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.Name != nil {
		c.Name = *row.Name
	}
	if row.Goal != nil {
		c.Goal = *row.Goal
	}
	if row.ChartData != nil {
		c.ChartData = *row.ChartData
	}
	if row.ChartType != nil {
		c.ChartType = *row.ChartType
	}
	if row.GenChart != nil {
		c.GenChart = *row.GenChart
	}
	if row.GenResult != nil {
		c.GenResult = *row.GenResult
	}
	if row.ExecMessage != nil {
		c.ExecMessage = *row.ExecMessage
	}
	return c
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ chart.Repository = (*Repo)(nil)
