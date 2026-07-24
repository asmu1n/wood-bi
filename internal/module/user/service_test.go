package user

import (
	"context"
	"testing"
	"time"

	"wood-bi/internal/pkg/page"
	"wood-bi/internal/pkg/response"
)

type memRepo struct {
	byID      map[int64]*User
	hashByID  map[int64]string
	nextID    int64
}

func newMemRepo() *memRepo {
	return &memRepo{
		byID:     make(map[int64]*User),
		hashByID: make(map[int64]string),
		nextID:   1,
	}
}

func (m *memRepo) Create(_ context.Context, account, passwordHash string, name, avatar *string, role string) (*User, error) {
	id := m.nextID
	m.nextID++
	u := &User{
		ID:        id,
		Account:   account,
		Role:      role,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if name != nil {
		u.Name = *name
	}
	if avatar != nil {
		u.Avatar = *avatar
	}
	m.byID[id] = u
	m.hashByID[id] = passwordHash
	return u, nil
}

func (m *memRepo) GetByID(_ context.Context, id int64) (*User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (m *memRepo) GetByAccount(_ context.Context, account string) (*User, error) {
	for _, u := range m.byID {
		if u.Account == account {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memRepo) GetCredentialByAccount(_ context.Context, account string) (*User, string, error) {
	for id, u := range m.byID {
		if u.Account == account {
			cp := *u
			return &cp, m.hashByID[id], nil
		}
	}
	return nil, "", nil
}

func (m *memRepo) UpdateProfile(_ context.Context, id int64, name, avatar *string) (*User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	if name != nil {
		u.Name = *name
	}
	if avatar != nil {
		u.Avatar = *avatar
	}
	cp := *u
	return &cp, nil
}

func (m *memRepo) UpdateAdmin(_ context.Context, id int64, name, avatar, role *string, passwordHash *string) (*User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, nil
	}
	if name != nil {
		u.Name = *name
	}
	if avatar != nil {
		u.Avatar = *avatar
	}
	if role != nil {
		u.Role = *role
	}
	if passwordHash != nil {
		m.hashByID[id] = *passwordHash
	}
	cp := *u
	return &cp, nil
}

func (m *memRepo) SoftDelete(_ context.Context, id int64) error {
	delete(m.byID, id)
	delete(m.hashByID, id)
	return nil
}

func (m *memRepo) ListPage(_ context.Context, q QueryParams) ([]*User, int64, error) {
	var all []*User
	for _, u := range m.byID {
		all = append(all, u)
	}
	return all, int64(len(all)), nil
}

func (m *memRepo) ExistsAccount(_ context.Context, account string) (bool, error) {
	for _, u := range m.byID {
		if u.Account == account {
			return true, nil
		}
	}
	return false, nil
}

func TestRegisterAndLogin(t *testing.T) {
	svc := NewService(newMemRepo())
	ctx := context.Background()

	id, err := svc.Register(ctx, RegisterInput{
		Account:       "alice",
		Password:      "password1",
		CheckPassword: "password1",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// 重复账号
	_, err = svc.Register(ctx, RegisterInput{
		Account:       "alice",
		Password:      "password1",
		CheckPassword: "password1",
	})
	if err == nil || !response.IsBizError(err) {
		t.Fatalf("expected biz error for duplicate account, got %v", err)
	}

	u, err := svc.Login(ctx, LoginInput{Account: "alice", Password: "password1"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.ID != id || u.Account != "alice" || u.Role != RoleUser {
		t.Fatalf("unexpected user: %+v", u)
	}

	_, err = svc.Login(ctx, LoginInput{Account: "alice", Password: "wrongpass"})
	if err == nil {
		t.Fatal("expected login failure")
	}
}

func TestRegisterValidation(t *testing.T) {
	svc := NewService(newMemRepo())
	ctx := context.Background()

	cases := []RegisterInput{
		{Account: "ab", Password: "password1", CheckPassword: "password1"},
		{Account: "alice", Password: "short", CheckPassword: "short"},
		{Account: "alice", Password: "password1", CheckPassword: "password2"},
	}
	for _, in := range cases {
		if _, err := svc.Register(ctx, in); err == nil {
			t.Fatalf("expected validation error for %+v", in)
		}
	}
}

func TestUpdateMyAndList(t *testing.T) {
	svc := NewService(newMemRepo())
	ctx := context.Background()

	id, err := svc.Register(ctx, RegisterInput{
		Account: "bob1234", Password: "password1", CheckPassword: "password1",
	})
	if err != nil {
		t.Fatal(err)
	}

	name := "Bob"
	u, err := svc.UpdateMy(ctx, id, UpdateMyInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Bob" {
		t.Fatalf("name=%q", u.Name)
	}

	pageResp, err := svc.ListPage(ctx, QueryParams{PageRequest: page.PageRequest{PageNum: 1, PageSize: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if pageResp.Total != 1 || len(pageResp.Records) != 1 {
		t.Fatalf("page=%+v", pageResp)
	}
}
