package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User 系统用户。
type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").
			Positive().
			Immutable(),
		field.String("account").
			NotEmpty().
			Unique().
			MaxLen(256).
			Comment("登录账号"),
		field.String("password_hash").
			NotEmpty().
			Sensitive().
			MaxLen(512).
			Comment("bcrypt 密码哈希"),
		field.String("name").
			Optional().
			Nillable().
			MaxLen(256).
			Comment("昵称"),
		field.String("avatar").
			Optional().
			Nillable().
			MaxLen(1024).
			Comment("头像 URL"),
		field.String("role").
			Default("user").
			MaxLen(32).
			Comment("角色：user / admin"),
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

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("charts", Chart.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account"),
	}
}
