package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
)

func TestSuspendServerBuildsASignedEvent(t *testing.T) {
	repo := &fakeModerationRepo{}
	s, err := newModerationService(repo).Suspend(context.Background(), "C3D4E5F6-0A1B-4C2D-9E3F-5A6B7C8D9E0F", testStaffID)
	if err != nil {
		t.Fatal(err)
	}

	if repo.key != admin.RoutingKeyServerSuspended {
		t.Errorf("routing key = %q", repo.key)
	}
	e, ok := repo.payload.(admin.ServerSuspended)
	if !ok {
		t.Fatalf("payload = %T, quería admin.ServerSuspended", repo.payload)
	}
	if e.ServerID != testServerID || e.Version != 4 || e.EventID != "evento-1" || !e.Verify(testSecret) || e.Verify("otro-secreto") {
		t.Errorf("evento: %+v", e)
	}
	// El id viaja normalizado, y la hora en UTC y en microsegundos, igual en la fila y en el evento.
	if repo.serverID != testServerID || repo.staffID != testStaffID {
		t.Errorf("repo recibió server=%q staff=%q", repo.serverID, repo.staffID)
	}
	if want := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC); !repo.at.Equal(want) || repo.at.Location() != time.UTC || !e.OccurredAt.Equal(repo.at) {
		t.Errorf("at = %v occurred_at = %v, quería %v", repo.at, e.OccurredAt, want)
	}
	if s == nil || s.ID != testServerID || s.SuspendedAt == nil {
		t.Errorf("tiene que devolver el servidor suspendido: %+v", s)
	}
}

func TestReactivateServerBuildsASignedEventWithTheNextVersion(t *testing.T) {
	repo := &fakeModerationRepo{}
	s, err := newModerationService(repo).Reactivate(context.Background(), testServerID)
	if err != nil {
		t.Fatal(err)
	}

	if repo.key != admin.RoutingKeyServerReactivated {
		t.Errorf("routing key = %q", repo.key)
	}
	e, ok := repo.payload.(admin.ServerReactivated)
	if !ok {
		t.Fatalf("payload = %T, quería admin.ServerReactivated", repo.payload)
	}
	if e.ServerID != testServerID || e.Version != 5 || !e.Verify(testSecret) {
		t.Errorf("evento: %+v", e)
	}
	if s == nil || s.ID != testServerID || s.SuspendedAt != nil {
		t.Errorf("tiene que devolver el servidor sin suspensión: %+v", s)
	}
}

// Una suspensión firmada no puede reenviarse como reactivación ni como pedido
// de eliminación: la routing key forma parte de lo firmado.
func TestSuspensionSignatureCannotBeReusedAsAnotherEvent(t *testing.T) {
	repo := &fakeModerationRepo{}
	if _, err := newModerationService(repo).Suspend(context.Background(), testServerID, testStaffID); err != nil {
		t.Fatal(err)
	}
	e := repo.payload.(admin.ServerSuspended)

	asReactivated := admin.ServerReactivated{Meta: e.Meta, ServerID: e.ServerID, Version: e.Version, Signature: e.Signature}
	asDeletion := admin.ServerDeletionRequested{Meta: e.Meta, ServerID: e.ServerID, Version: e.Version, Signature: e.Signature}
	if asReactivated.Verify(testSecret) || asDeletion.Verify(testSecret) {
		t.Error("la firma de una suspensión no debe valer para otro evento")
	}
}

func TestSuspendAndReactivateTreatAnInvalidIDAsNotFoundWithoutTouchingTheRepository(t *testing.T) {
	for _, id := range []string{"", "no-es-un-uuid", "123"} {
		repo := &fakeModerationRepo{}
		svc := newModerationService(repo)

		if _, err := svc.Suspend(context.Background(), id, testStaffID); !errors.Is(err, ErrServerNotFound) {
			t.Errorf("Suspend(%q): err = %v, quería ErrServerNotFound", id, err)
		}
		if _, err := svc.Reactivate(context.Background(), id); !errors.Is(err, ErrServerNotFound) {
			t.Errorf("Reactivate(%q): err = %v, quería ErrServerNotFound", id, err)
		}
		if repo.called {
			t.Errorf("id %q: no debió consultar el repositorio", id)
		}
	}
}

func TestSuspendAndReactivateExposeTheBusinessErrors(t *testing.T) {
	cases := map[string]struct{ in, want error }{
		"no existe":          {repository.ErrServerNotFound, ErrServerNotFound},
		"ya está suspendido": {repository.ErrServerAlreadySuspended, ErrServerAlreadySuspended},
		"no está suspendido": {repository.ErrServerNotSuspended, ErrServerNotSuspended},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newModerationService(&fakeModerationRepo{err: c.in})
			if _, err := svc.Suspend(context.Background(), testServerID, testStaffID); !errors.Is(err, c.want) {
				t.Errorf("Suspend: err = %v, quería %v", err, c.want)
			}
			if _, err := svc.Reactivate(context.Background(), testServerID); !errors.Is(err, c.want) {
				t.Errorf("Reactivate: err = %v, quería %v", err, c.want)
			}
		})
	}
}

func TestSuspendWrapsUnexpectedErrors(t *testing.T) {
	boom := errors.New("base caída")
	_, err := newModerationService(&fakeModerationRepo{err: boom}).Suspend(context.Background(), testServerID, testStaffID)
	if !errors.Is(err, boom) || errors.Is(err, ErrServerNotFound) || errors.Is(err, ErrServerAlreadySuspended) {
		t.Errorf("tiene que conservar la causa y no ser un error de negocio: %v", err)
	}
}

func TestRepublishServerSuspensionsPublishesOneSignedEventPerSuspendedServer(t *testing.T) {
	first := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	second := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	repo := &fakeModerationRepo{suspended: []repository.SuspendedServer{
		{ServerID: testServerID, Version: 1, SuspendedAt: first},
		{ServerID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Version: 3, SuspendedAt: second},
	}}
	pub := &fakePublisher{}

	n, err := newModerationService(repo).RepublishServerSuspensions(context.Background(), pub)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for i, want := range repo.suspended {
		if pub.keys[i] != admin.RoutingKeyServerSuspended {
			t.Errorf("routing key %d = %q", i, pub.keys[i])
		}
		e, ok := pub.payloads[i].(admin.ServerSuspended)
		if !ok {
			t.Fatalf("payload %d: %T", i, pub.payloads[i])
		}
		if e.ServerID != want.ServerID || e.Version != want.Version || !e.OccurredAt.Equal(want.SuspendedAt) || !e.Verify(testSecret) {
			t.Errorf("evento %d: %+v", i, e)
		}
	}
}

func TestRepublishServerSuspensionsWithNothingSuspendedPublishesNothing(t *testing.T) {
	pub := &fakePublisher{}
	n, err := newModerationService(&fakeModerationRepo{}).RepublishServerSuspensions(context.Background(), pub)
	if err != nil || n != 0 || len(pub.keys) != 0 {
		t.Errorf("n=%d err=%v publicados=%d", n, err, len(pub.keys))
	}
}

func TestRepublishServerSuspensionsStopsAtTheFirstPublishError(t *testing.T) {
	repo := &fakeModerationRepo{suspended: []repository.SuspendedServer{{ServerID: "s1", Version: 1}, {ServerID: "s2", Version: 1}, {ServerID: "s3", Version: 1}}}
	boom := errors.New("broker caído")
	pub := &fakePublisher{failAt: 2, err: boom}

	n, err := newModerationService(repo).RepublishServerSuspensions(context.Background(), pub)
	if !errors.Is(err, boom) || n != 1 || len(pub.keys) != 1 {
		t.Errorf("n=%d err=%v publicados=%d, quería cortar tras 1", n, err, len(pub.keys))
	}
}

func TestRepublishServerSuspensionsPropagatesTheListError(t *testing.T) {
	boom := errors.New("db caída")
	if _, err := newModerationService(&fakeModerationRepo{err: boom}).RepublishServerSuspensions(context.Background(), &fakePublisher{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
