package repo

import (
	"context"
	"time"

	"wood-bi/ent"
	entuser "wood-bi/ent/user"
	"wood-bi/internal/module/user"

	"entgo.io/ent/dialect/sql"
)

// repo 基于 Ent 的用户仓储。
type repo struct {
	client *ent.Client
}

func New(client *ent.Client) user.Repository {
	return &repo{client: client}
}

func (r *repo) Create(ctx context.Context, account, passwordHash string, name, avatar *string, role string) (*user.User, error) {
	builder := r.client.User.Create().
		SetAccount(account).
		SetPasswordHash(passwordHash).
		SetRole(role).
		SetNillableName(name).
		SetNillableAvatar(avatar)

	row, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, err
		}
		return nil, err
	}
	return toDomain(row), nil
}

func (r *repo) GetByID(ctx context.Context, id int64) (*user.User, error) {
	row, err := r.client.User.Query().
		Where(
			entuser.IDEQ(id),
			entuser.DeletedAtIsNil(),
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

func (r *repo) GetByAccount(ctx context.Context, account string) (*user.User, error) {
	row, err := r.client.User.Query().
		Where(
			entuser.AccountEQ(account),
			entuser.DeletedAtIsNil(),
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

func (r *repo) GetCredentialByAccount(ctx context.Context, account string) (*user.User, string, error) {
	row, err := r.client.User.Query().
		Where(
			entuser.AccountEQ(account),
			entuser.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return toDomain(row), row.PasswordHash, nil
}

func (r *repo) UpdateProfile(ctx context.Context, id int64, name, avatar *string) (*user.User, error) {
	upd := r.client.User.UpdateOneID(id).
		Where(entuser.DeletedAtIsNil())
	if name != nil {
		upd.SetName(*name)
	}
	if avatar != nil {
		upd.SetAvatar(*avatar)
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

func (r *repo) UpdateAdmin(ctx context.Context, id int64, name, avatar, role *string, passwordHash *string) (*user.User, error) {
	upd := r.client.User.UpdateOneID(id).
		Where(entuser.DeletedAtIsNil())
	if name != nil {
		upd.SetName(*name)
	}
	if avatar != nil {
		upd.SetAvatar(*avatar)
	}
	if role != nil {
		upd.SetRole(*role)
	}
	if passwordHash != nil {
		upd.SetPasswordHash(*passwordHash)
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

func (r *repo) SoftDelete(ctx context.Context, id int64) error {
	now := time.Now()
	n, err := r.client.User.Update().
		Where(
			entuser.IDEQ(id),
			entuser.DeletedAtIsNil(),
		).
		SetDeletedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return nil
}

func (r *repo) ListPage(ctx context.Context, q user.QueryParams) ([]*user.User, int64, error) {
	query := r.client.User.Query().Where(entuser.DeletedAtIsNil())
	if q.ID > 0 {
		query = query.Where(entuser.IDEQ(q.ID))
	}
	if q.Account != "" {
		query = query.Where(entuser.AccountContains(q.Account))
	}
	if q.Name != "" {
		query = query.Where(entuser.NameContains(q.Name))
	}
	if q.Role != "" {
		query = query.Where(entuser.RoleEQ(q.Role))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	rows, err := query.
		Order(entuser.ByCreatedAt(sql.OrderDesc())).
		Offset(q.Offset()).
		Limit(q.Limit()).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}

	out := make([]*user.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, int64(total), nil
}

func (r *repo) ExistsAccount(ctx context.Context, account string) (bool, error) {
	return r.client.User.Query().
		Where(
			entuser.AccountEQ(account),
			entuser.DeletedAtIsNil(),
		).
		Exist(ctx)
}

func toDomain(row *ent.User) *user.User {
	if row == nil {
		return nil
	}
	u := &user.User{
		ID:        row.ID,
		Account:   row.Account,
		Role:      row.Role,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		DeletedAt: row.DeletedAt,
	}
	if row.Name != nil {
		u.Name = *row.Name
	}
	if row.Avatar != nil {
		u.Avatar = *row.Avatar
	}
	return u
}
