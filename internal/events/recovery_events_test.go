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

type fakeRecoverySync struct {
	records []repository.RecoveryRequestRecord
	err     error
}

func (f *fakeRecoverySync) ApplyRequest(_ context.Context, r repository.RecoveryRequestRecord) error {
	f.records = append(f.records, r)
	return f.err
}

const recoveryRequestID = "5b2d7c1e-3a9f-4e8b-b6c4-1f2e3d4c5b6a"

func TestAccountRecoveryRequestedIsApplied(t *testing.T) {
	recovery := &fakeRecoverySync{}
	body := `{"event_id":"e1","occurred_at":"2026-11-12T14:30:00Z","request_id":"5B2D7C1E-3A9F-4E8B-B6C4-1F2E3D4C5B6A","user_id":"` + validID + `","message":"cambié de celular","requested_at":"2026-11-12T11:30:00-03:00"}`

	if err := NewUserEventsHandler(&fakeSync{}).WithRecoveryRequests(recovery).Handle(context.Background(), users.RoutingKeyAccountRecoveryRequested, []byte(body)); err != nil {
		t.Fatal(err)
	}

	want := repository.RecoveryRequestRecord{
		RequestID:   recoveryRequestID,
		UserID:      validID,
		Message:     "cambié de celular",
		RequestedAt: time.Date(2026, 11, 12, 14, 30, 0, 0, time.UTC),
	}
	if len(recovery.records) != 1 || recovery.records[0] != want {
		t.Errorf("solicitudes aplicadas = %+v, quería [%+v]", recovery.records, want)
	}
}

func TestAccountRecoveryRequestedDiscardsInvalidMessages(t *testing.T) {
	cases := map[string]struct {
		body string
		want audit.Status
	}{
		"json roto":        {`{`, audit.StatusDiscardedMalformed},
		"request_id malo":  {`{"request_id":"x","user_id":"` + validID + `","requested_at":"2026-11-12T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		"user_id malo":     {`{"request_id":"` + recoveryRequestID + `","user_id":"x","requested_at":"2026-11-12T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		"sin requested_at": {`{"request_id":"` + recoveryRequestID + `","user_id":"` + validID + `"}`, audit.StatusDiscardedInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recovery := &fakeRecoverySync{}
			err := NewUserEventsHandler(&fakeSync{}).WithRecoveryRequests(recovery).Handle(context.Background(), users.RoutingKeyAccountRecoveryRequested, []byte(tc.body))
			if got := status(t, err); got != tc.want {
				t.Errorf("status = %q, quería %q", got, tc.want)
			}
			if len(recovery.records) != 0 {
				t.Error("no debería guardar nada")
			}
		})
	}
}

func TestAccountRecoveryRequestedRequeuesOnDatabaseFailure(t *testing.T) {
	recovery := &fakeRecoverySync{err: errors.New("db caída")}
	body := `{"request_id":"` + recoveryRequestID + `","user_id":"` + validID + `","requested_at":"2026-11-12T14:30:00Z"}`

	err := NewUserEventsHandler(&fakeSync{}).WithRecoveryRequests(recovery).Handle(context.Background(), users.RoutingKeyAccountRecoveryRequested, []byte(body))

	var discard *DiscardError
	if err == nil || errors.As(err, &discard) {
		t.Fatalf("una falla de base tiene que reencolarse, no descartarse: %v", err)
	}
}

func TestAccountRecoveryRequestedWithoutSinkIsUnknown(t *testing.T) {
	body := `{"request_id":"` + recoveryRequestID + `","user_id":"` + validID + `","requested_at":"2026-11-12T14:30:00Z"}`

	err := NewUserEventsHandler(&fakeSync{}).Handle(context.Background(), users.RoutingKeyAccountRecoveryRequested, []byte(body))

	if got := status(t, err); got != audit.StatusDiscardedUnknownKey {
		t.Errorf("status = %q, quería %q", got, audit.StatusDiscardedUnknownKey)
	}
}

func TestUserBindingsIncludeRecoveryRequests(t *testing.T) {
	for _, key := range UserBindings {
		if key == users.RoutingKeyAccountRecoveryRequested {
			return
		}
	}
	t.Error("la cola admin.users tiene que recibir users.account_recovery_requested")
}
