package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Chart 图表 / AI 分析任务。
type Chart struct {
	ent.Schema
}

func (Chart) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").
			Positive().
			Immutable(),
		field.String("name").
			Optional().
			Nillable().
			MaxLen(128).
			Comment("图表名称"),
		field.Text("goal").
			Optional().
			Nillable().
			Comment("分析目标"),
		field.Text("chart_data").
			Optional().
			Nillable().
			Comment("原始 CSV 数据"),
		field.String("chart_type").
			Optional().
			Nillable().
			MaxLen(128).
			Comment("图表类型"),
		field.Text("gen_chart").
			Optional().
			Nillable().
			Comment("AI 生成的 ECharts option JSON"),
		field.Text("gen_result").
			Optional().
			Nillable().
			Comment("AI 分析结论"),
		field.String("status").
			Default("wait").
			MaxLen(32).
			Comment("wait / running / succeed / failed"),
		field.Text("exec_message").
			Optional().
			Nillable().
			Comment("执行信息（失败原因等）"),
		field.Int64("user_id").
			Comment("创建用户 ID"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Time("deleted_at").
			Optional().
			Nillable().
			Comment("软删除时间"),
	}
}

func (Chart) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).
			Ref("charts").
			Field("user_id").
			Unique().
			Required(),
	}
}

func (Chart) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
		index.Fields("status"),
	}
}
