package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

type fakeServerRepo struct {
	servers []model.Server
	total   int
	err     error
	filter  repository.ServerFilter
	limit   int
	offset  int

	snapshot   *repository.ServerSnapshot
	deletedID  string
	deletedAt  time.Time
	membership *repository.Membership
}

func (f *fakeServerRepo) List(_ context.Context, filter repository.ServerFilter, limit, offset int) ([]model.Server, int, error) {
	f.filter, f.limit, f.offset = filter, limit, offset
	return f.servers, f.total, f.err
}

func (f *fakeServerRepo) UpsertServer(_ context.Context, s repository.ServerSnapshot) error {
	f.snapshot = &s
	return f.err
}

func (f *fakeServerRepo) MarkDeleted(_ context.Context, id string, at time.Time) error {
	f.deletedID, f.deletedAt = id, at
	return f.err
}

func (f *fakeServerRepo) SetMembership(_ context.Context, m repository.Membership) error {
	f.membership = &m
	return f.err
}

func TestServerApplyDelegatesToRepository(t *testing.T) {
	repo := &fakeServerRepo{}
	svc := NewServerService(repo)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	snap := repository.ServerSnapshot{ServerID: "s1", Name: "S", OwnerID: "u1", CreatedAt: at, OccurredAt: at}
	member := repository.Membership{ServerID: "s1", UserID: "u2", IsMember: true, OccurredAt: at}

	if err := svc.ApplyServerUpdated(ctx, snap); err != nil || repo.snapshot == nil || *repo.snapshot != snap {
		t.Errorf("ApplyServerUpdated: err=%v repo=%+v", err, repo.snapshot)
	}
	if err := svc.ApplyServerDeleted(ctx, "s1", at); err != nil || repo.deletedID != "s1" || !repo.deletedAt.Equal(at) {
		t.Errorf("ApplyServerDeleted: err=%v id=%q at=%v", err, repo.deletedID, repo.deletedAt)
	}
	if err := svc.ApplyMembership(ctx, member); err != nil || repo.membership == nil || *repo.membership != member {
		t.Errorf("ApplyMembership: err=%v repo=%+v", err, repo.membership)
	}

	boom := errors.New("boom")
	repo.err = boom
	if err := svc.ApplyServerUpdated(ctx, snap); !errors.Is(err, boom) {
		t.Errorf("ApplyServerUpdated debe propagar el error: %v", err)
	}
	if err := svc.ApplyServerDeleted(ctx, "s1", at); !errors.Is(err, boom) {
		t.Errorf("ApplyServerDeleted debe propagar el error: %v", err)
	}
	if err := svc.ApplyMembership(ctx, member); !errors.Is(err, boom) {
		t.Errorf("ApplyMembership debe propagar el error: %v", err)
	}
}

func TestServerListPassesFilterAndPagination(t *testing.T) {
	repo := &fakeServerRepo{servers: []model.Server{{ID: "s1"}}, total: 41}
	list, err := NewServerService(repo).List(context.Background(), ListServersParams{Search: "gam", Limit: 10, Offset: 30})
	if err != nil {
		t.Fatal(err)
	}
	if repo.filter.Search != "gam" || repo.limit != 10 || repo.offset != 30 {
		t.Errorf("filtro mal pasado: %+v limit=%d offset=%d", repo.filter, repo.limit, repo.offset)
	}
	if list.Total != 41 || list.Limit != 10 || list.Offset != 30 || len(list.Servers) != 1 {
		t.Errorf("respuesta: %+v", list)
	}
}

func TestServerListNormalizesLimit(t *testing.T) {
	cases := map[int]int{0: model.DefaultLimit, -5: model.DefaultLimit, 1: 1, 100: 100, 5000: model.MaxLimit}
	for in, want := range cases {
		repo := &fakeServerRepo{}
		list, err := NewServerService(repo).List(context.Background(), ListServersParams{Limit: in})
		if err != nil {
			t.Fatal(err)
		}
		if repo.limit != want || list.Limit != want {
			t.Errorf("limit %d: repo=%d respuesta=%d, quería %d", in, repo.limit, list.Limit, want)
		}
	}
}

func TestServerListWrapsRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := NewServerService(&fakeServerRepo{err: boom}).List(context.Background(), ListServersParams{}); !errors.Is(err, boom) {
		t.Fatalf("esperaba el error de la base envuelto, obtuve %v", err)
	}
}
