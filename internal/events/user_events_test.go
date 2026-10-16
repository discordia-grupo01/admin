package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/users"
)

const validID = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"

type fakeSync struct {
	registrations []repository.Registration
	profiles      []repository.Profile
	err           error
}

func (f *fakeSync) ApplyRegistration(_ context.Context, r repository.Registration) error {
	f.registrations = append(f.registrations, r)
	return f.err
}

func (f *fakeSync) ApplyProfile(_ context.Context, p repository.Profile) error {
	f.profiles = append(f.profiles, p)
	return f.err
}

func status(t *testing.T, err error) audit.Status {
	t.Helper()
	var d *DiscardError
	if !errors.As(err, &d) {
		t.Fatalf("esperaba un DiscardError, obtuve %v", err)
	}
	return d.Status
}

func TestAccountRegisteredIsApplied(t *testing.T) {
	sync := &fakeSync{}
	body := `{"event_id":"e1","occurred_at":"2026-09-26T14:30:00Z","user_id":"7C1E5A3B-9D2F-4B8E-A6C4-3F1E9D7B5A2C","email":"  manu@example.com ","name":"Manu","created_at":"2026-09-26T11:30:00-03:00"}`
	if err := NewUserEventsHandler(sync).Handle(context.Background(), users.RoutingKeyAccountRegistered, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(sync.registrations) != 1 {
		t.Fatalf("registros aplicados: %+v", sync.registrations)
	}
	got := sync.registrations[0]
	if got.UserID != validID {
		t.Errorf("el id debe quedar en forma canónica (minúsculas): %q", got.UserID)
	}
	if got.Email != "manu@example.com" || got.Name != "Manu" {
		t.Errorf("campos: %+v", got)
	}
	if want := time.Date(2026, 9, 26, 14, 30, 0, 0, time.UTC); !got.RegisteredAt.Equal(want) || got.RegisteredAt.Location() != time.UTC {
		t.Errorf("RegisteredAt = %v, quería %v en UTC", got.RegisteredAt, want)
	}
}

func TestProfileUpdatedIsApplied(t *testing.T) {
	sync := &fakeSync{}
	body := `{"event_id":"e2","occurred_at":"2026-09-28T14:30:00Z","user_id":"` + validID + `","version":3,"name":"Manu P","avatar_url":"","description":"","status_text":"","status_emoji":""}`
	if err := NewUserEventsHandler(sync).Handle(context.Background(), users.RoutingKeyProfileUpdated, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(sync.profiles) != 1 || sync.profiles[0] != (repository.Profile{UserID: validID, Name: "Manu P", Version: 3}) {
		t.Errorf("perfiles aplicados: %+v", sync.profiles)
	}
}

func TestInvalidEventsAreDiscardedWithoutTouchingTheStore(t *testing.T) {
	cases := []struct {
		name, key, body string
		want            audit.Status
	}{
		{"registro: JSON roto", users.RoutingKeyAccountRegistered, `{no es json`, audit.StatusDiscardedMalformed},
		{"perfil: JSON roto", users.RoutingKeyProfileUpdated, `[]`, audit.StatusDiscardedMalformed},
		{"registro: id que no es UUID", users.RoutingKeyAccountRegistered, `{"user_id":"user-1","email":"a@b.c","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"registro: id vacío", users.RoutingKeyAccountRegistered, `{"email":"a@b.c","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"registro: email vacío", users.RoutingKeyAccountRegistered, `{"user_id":"` + validID + `","email":"   ","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"registro: sin fecha", users.RoutingKeyAccountRegistered, `{"user_id":"` + validID + `","email":"a@b.c"}`, audit.StatusDiscardedInvalid},
		{"registro: fecha mal formada", users.RoutingKeyAccountRegistered, `{"user_id":"` + validID + `","email":"a@b.c","created_at":"ayer"}`, audit.StatusDiscardedMalformed},
		{"perfil: id que no es UUID", users.RoutingKeyProfileUpdated, `{"user_id":"x","version":1,"name":"n"}`, audit.StatusDiscardedInvalid},
		{"perfil: versión 0", users.RoutingKeyProfileUpdated, `{"user_id":"` + validID + `","version":0,"name":"n"}`, audit.StatusDiscardedInvalid},
		{"perfil: versión negativa", users.RoutingKeyProfileUpdated, `{"user_id":"` + validID + `","version":-2,"name":"n"}`, audit.StatusDiscardedInvalid},
		{"clave desconocida", "users.otra_cosa", `{}`, audit.StatusDiscardedUnknownKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sync := &fakeSync{}
			err := NewUserEventsHandler(sync).Handle(context.Background(), tc.key, []byte(tc.body))
			if got := status(t, err); got != tc.want {
				t.Errorf("status = %s, quería %s", got, tc.want)
			}
			if len(sync.registrations)+len(sync.profiles) != 0 {
				t.Error("un evento inválido no debe llegar a la base")
			}
		})
	}
}

// Una falla de base NO es un descarte: tiene que devolverse tal cual para que
// el consumer reencole el mensaje y no se pierda el evento.
func TestStoreErrorIsTransientNotDiscarded(t *testing.T) {
	boom := errors.New("db caída")
	for _, key := range []string{users.RoutingKeyAccountRegistered, users.RoutingKeyProfileUpdated} {
		body := `{"user_id":"` + validID + `","email":"a@b.c","name":"n","version":1,"created_at":"2026-09-26T14:30:00Z"}`
		err := NewUserEventsHandler(&fakeSync{err: boom}).Handle(context.Background(), key, []byte(body))
		if !errors.Is(err, boom) {
			t.Fatalf("%s: err = %v, quería el error de la base", key, err)
		}
		var d *DiscardError
		if errors.As(err, &d) {
			t.Errorf("%s: un error de base no debe marcarse como descarte", key)
		}
		if !Process(context.Background(), NewUserEventsHandler(&fakeSync{err: boom}), &recorderSpy{}, key, []byte(body)) {
			t.Errorf("%s: Process debe pedir reencolar ante un error de base", key)
		}
	}
}
