package service

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

type fakeUserRepo struct {
	users  []model.User
	total  int
	err    error
	filter repository.UserFilter
	limit  int
	offset int
	called bool
}

func (f *fakeUserRepo) List(_ context.Context, filter repository.UserFilter, limit, offset int) ([]model.User, int, error) {
	f.called = true
	f.filter, f.limit, f.offset = filter, limit, offset
	return f.users, f.total, f.err
}

func TestListPassesFiltersAndPagination(t *testing.T) {
	repo := &fakeUserRepo{users: []model.User{{ID: "u1"}}, total: 41}
	list, err := NewUserService(repo).List(context.Background(), ListUsersParams{
		Search: "manu", Status: model.UserStatusSuspended, Limit: 10, Offset: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.filter.Search != "manu" || repo.filter.Status != model.UserStatusSuspended || repo.limit != 10 || repo.offset != 30 {
		t.Errorf("filtro mal pasado al repositorio: %+v limit=%d offset=%d", repo.filter, repo.limit, repo.offset)
	}
	if list.Total != 41 || list.Limit != 10 || list.Offset != 30 || len(list.Users) != 1 {
		t.Errorf("respuesta: %+v", list)
	}
}

func TestListNormalizesLimit(t *testing.T) {
	cases := map[int]int{0: model.DefaultLimit, -5: model.DefaultLimit, 1: 1, 100: 100, 101: model.MaxLimit, 5000: model.MaxLimit}
	for in, want := range cases {
		repo := &fakeUserRepo{}
		list, err := NewUserService(repo).List(context.Background(), ListUsersParams{Limit: in})
		if err != nil {
			t.Fatal(err)
		}
		if repo.limit != want || list.Limit != want {
			t.Errorf("limit %d: repo=%d respuesta=%d, quería %d", in, repo.limit, list.Limit, want)
		}
	}
}

func TestListRejectsInvalidStatusWithoutQuerying(t *testing.T) {
	repo := &fakeUserRepo{}
	_, err := NewUserService(repo).List(context.Background(), ListUsersParams{Status: "banned"})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("err = %v, quería ErrInvalidStatus", err)
	}
	if repo.called {
		t.Error("un filtro inválido no debe llegar a la base")
	}
}

func TestListAcceptsEmptyAndKnownStatus(t *testing.T) {
	for _, s := range []model.UserStatus{"", model.UserStatusActive, model.UserStatusSuspended} {
		if _, err := NewUserService(&fakeUserRepo{}).List(context.Background(), ListUsersParams{Status: s}); err != nil {
			t.Errorf("status %q: %v", s, err)
		}
	}
}

func TestListWrapsRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	_, err := NewUserService(&fakeUserRepo{err: boom}).List(context.Background(), ListUsersParams{})
	if !errors.Is(err, boom) {
		t.Fatalf("esperaba el error de la base envuelto, obtuve %v", err)
	}
}
