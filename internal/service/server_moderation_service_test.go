package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
)

const testServerID = "c3d4e5f6-0a1b-4c2d-9e3f-5a6b7c8d9e0f"

type fakeModerationRepo struct {
	err error

	called      bool
	serverID    string
	confirmName string
	staffID     string
	at          time.Time
	key         string
	payload     any
}

func (f *fakeModerationRepo) RequestDeletion(_ context.Context, serverID, confirmName, staffID string, at time.Time, event repository.EventBuilder) error {
	f.called = true
	f.serverID, f.confirmName, f.staffID, f.at = serverID, confirmName, staffID, at
	if f.err != nil {
		return f.err
	}
	f.key, f.payload = event(4)
	return nil
}

func newModerationService(repo repository.ServerModerationRepository) *ServerModerationService {
	s := NewServerModerationService(repo, testSecret)
	s.now = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 123, time.FixedZone("ART", -3*3600)) }
	s.newEventID = func() string { return "evento-1" }
	return s
}

func TestDeleteBuildsASignedEventWithTheVersionFromTheRepository(t *testing.T) {
	repo := &fakeModerationRepo{}
	if err := newModerationService(repo).Delete(context.Background(), testServerID, "Sala", testStaffID); err != nil {
		t.Fatal(err)
	}

	if repo.key != admin.RoutingKeyServerDeletionRequested {
		t.Errorf("routing key = %q", repo.key)
	}
	e, ok := repo.payload.(admin.ServerDeletionRequested)
	if !ok {
		t.Fatalf("payload = %T, quería admin.ServerDeletionRequested", repo.payload)
	}
	if e.ServerID != testServerID || e.Version != 4 || e.EventID != "evento-1" {
		t.Errorf("evento: %+v", e)
	}
	if !e.Verify(testSecret) {
		t.Error("el evento tiene que salir firmado con el secreto del servicio")
	}
	if e.Verify("otro-secreto") {
		t.Error("la firma no debe valer con otro secreto")
	}
}

func TestDeletePassesWhatTheRepositoryNeeds(t *testing.T) {
	repo := &fakeModerationRepo{}
	if err := newModerationService(repo).Delete(context.Background(), testServerID, "  Sala ", testStaffID); err != nil {
		t.Fatal(err)
	}

	if repo.serverID != testServerID || repo.confirmName != "  Sala " || repo.staffID != testStaffID {
		t.Errorf("repo recibió server=%q confirm=%q staff=%q", repo.serverID, repo.confirmName, repo.staffID)
	}
	// Postgres guarda microsegundos y la hora se pasa en UTC: el evento y la
	// fila tienen que mostrar la misma.
	if want := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC); !repo.at.Equal(want) || repo.at.Location() != time.UTC {
		t.Errorf("at = %v, quería %v", repo.at, want)
	}
	e := repo.payload.(admin.ServerDeletionRequested)
	if !e.OccurredAt.Equal(repo.at) {
		t.Errorf("occurred_at del evento = %v, quería %v", e.OccurredAt, repo.at)
	}
}

func TestDeleteNormalizesTheServerID(t *testing.T) {
	repo := &fakeModerationRepo{}
	if err := newModerationService(repo).Delete(context.Background(), "C3D4E5F6-0A1B-4C2D-9E3F-5A6B7C8D9E0F", "Sala", testStaffID); err != nil {
		t.Fatal(err)
	}
	if repo.serverID != testServerID {
		t.Errorf("el id se pasa en minúsculas: %q", repo.serverID)
	}
	if e := repo.payload.(admin.ServerDeletionRequested); e.ServerID != testServerID {
		t.Errorf("el evento lleva el id normalizado: %q", e.ServerID)
	}
}

func TestDeleteTreatsAnInvalidIDAsNotFoundWithoutTouchingTheRepository(t *testing.T) {
	for _, id := range []string{"", "no-es-un-uuid", "123", "c3d4e5f6-0a1b-4c2d-9e3f"} {
		repo := &fakeModerationRepo{}
		err := newModerationService(repo).Delete(context.Background(), id, "Sala", testStaffID)
		if !errors.Is(err, ErrServerNotFound) {
			t.Errorf("id %q: err = %v, quería ErrServerNotFound", id, err)
		}
		if repo.called {
			t.Errorf("id %q: no debió consultar el repositorio", id)
		}
	}
}

func TestDeleteExposesTheBusinessErrors(t *testing.T) {
	cases := map[string]struct{ in, want error }{
		"no existe":       {repository.ErrServerNotFound, ErrServerNotFound},
		"nombre distinto": {repository.ErrDeleteConfirmationMismatch, ErrDeleteConfirmationMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := newModerationService(&fakeModerationRepo{err: c.in}).Delete(context.Background(), testServerID, "Sala", testStaffID)
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, quería %v", err, c.want)
			}
		})
	}
}

func TestDeleteWrapsUnexpectedErrors(t *testing.T) {
	boom := errors.New("base caída")
	err := newModerationService(&fakeModerationRepo{err: boom}).Delete(context.Background(), testServerID, "Sala", testStaffID)
	if !errors.Is(err, boom) {
		t.Errorf("tiene que conservar la causa: %v", err)
	}
	if errors.Is(err, ErrServerNotFound) || errors.Is(err, ErrDeleteConfirmationMismatch) {
		t.Errorf("un error inesperado no es un error de negocio: %v", err)
	}
}
