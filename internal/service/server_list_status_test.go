package service

import (
	"context"
	"errors"
	"testing"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

func TestServerListPassesTheStatusFilterToTheRepository(t *testing.T) {
	for _, status := range []model.ServerStatus{model.ServerStatusActive, model.ServerStatusSuspended} {
		repo := &fakeServerRepo{}
		_, err := NewServerService(repo).List(context.Background(), ListServersParams{Search: "sala", Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if want := (repository.ServerFilter{Search: "sala", Status: status}); repo.filter != want {
			t.Errorf("filtro = %+v, quería %+v", repo.filter, want)
		}
	}
}

func TestServerListWithoutStatusDoesNotFilterByIt(t *testing.T) {
	repo := &fakeServerRepo{}
	if _, err := NewServerService(repo).List(context.Background(), ListServersParams{}); err != nil {
		t.Fatal(err)
	}
	if repo.filter.Status != "" {
		t.Errorf("status = %q, quería vacío", repo.filter.Status)
	}
}

func TestServerListRejectsAnUnknownStatusWithoutQuerying(t *testing.T) {
	for _, status := range []model.ServerStatus{"pending", "SUSPENDED", "borrado", " "} {
		repo := &fakeServerRepo{}
		_, err := NewServerService(repo).List(context.Background(), ListServersParams{Status: status})
		if !errors.Is(err, ErrInvalidServerStatus) {
			t.Errorf("status %q: err = %v, quería ErrInvalidServerStatus", status, err)
		}
		if repo.limit != 0 {
			t.Errorf("status %q: no debió consultar el repositorio", status)
		}
	}
}
