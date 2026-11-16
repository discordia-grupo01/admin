package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/membership"
	"github.com/discordia-grupo01/events-contract/moderation"
	"github.com/discordia-grupo01/events-contract/reports"
	"github.com/discordia-grupo01/events-contract/servers"
)

const (
	serverID = "3b9a6c1e-5d2f-4a8e-b7c4-1f9e3d5b7a2c"
	otherID  = "9d4e7a2b-1c3f-4e6a-8b5d-2a7c9e1f3b4d"
)

type fakeServerSync struct {
	snapshots   []repository.ServerSnapshot
	deleted     []string
	deletedAt   []time.Time
	memberships []repository.Membership
	err         error
}

func (f *fakeServerSync) ApplyServerUpdated(_ context.Context, s repository.ServerSnapshot) error {
	f.snapshots = append(f.snapshots, s)
	return f.err
}

func (f *fakeServerSync) ApplyServerDeleted(_ context.Context, id string, at time.Time) error {
	f.deleted = append(f.deleted, id)
	f.deletedAt = append(f.deletedAt, at)
	return f.err
}

func (f *fakeServerSync) ApplyMembership(_ context.Context, m repository.Membership) error {
	f.memberships = append(f.memberships, m)
	return f.err
}

func (f *fakeServerSync) touched() int {
	return len(f.snapshots) + len(f.deleted) + len(f.memberships)
}

func TestServerUpdatedIsApplied(t *testing.T) {
	sync := &fakeServerSync{}
	body := `{"event_id":"e1","occurred_at":"2026-09-26T14:30:00Z","server_id":"3B9A6C1E-5D2F-4A8E-B7C4-1F9E3D5B7A2C","name":"  Gamers  ","owner_id":"` + otherID + `","created_at":"2026-09-26T11:00:00-03:00"}`
	if err := NewServerEventsHandler(sync).Handle(context.Background(), servers.RoutingKeyServerUpdated, []byte(body)); err != nil {
		t.Fatal(err)
	}
	want := repository.ServerSnapshot{
		ServerID:   serverID,
		Name:       "Gamers",
		OwnerID:    otherID,
		CreatedAt:  time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC),
		OccurredAt: time.Date(2026, 9, 26, 14, 30, 0, 0, time.UTC),
	}
	if len(sync.snapshots) != 1 || !snapshotEqual(sync.snapshots[0], want) {
		t.Fatalf("foto aplicada: %+v, quería %+v", sync.snapshots, want)
	}
	if sync.snapshots[0].CreatedAt.Location() != time.UTC || sync.snapshots[0].OccurredAt.Location() != time.UTC {
		t.Error("las fechas deben quedar en UTC")
	}
}

func snapshotEqual(a, b repository.ServerSnapshot) bool {
	return a.ServerID == b.ServerID && a.Name == b.Name && a.OwnerID == b.OwnerID && a.IconURL == b.IconURL &&
		a.CreatedAt.Equal(b.CreatedAt) && a.OccurredAt.Equal(b.OccurredAt)
}

func TestServerDeletedIsApplied(t *testing.T) {
	sync := &fakeServerSync{}
	body := `{"event_id":"e2","occurred_at":"2026-09-27T10:00:00Z","server_id":"` + serverID + `","channel_ids":["c1"]}`
	if err := NewServerEventsHandler(sync).Handle(context.Background(), servers.RoutingKeyServerDeleted, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(sync.deleted) != 1 || sync.deleted[0] != serverID || !sync.deletedAt[0].Equal(time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("borrados: %v en %v", sync.deleted, sync.deletedAt)
	}
}

func TestMemberJoinedAndLeftSetMembership(t *testing.T) {
	body := `{"event_id":"e3","occurred_at":"2026-09-27T10:00:00Z","server_id":"` + serverID + `","user_id":"` + otherID + `"}`
	for key, wantMember := range map[string]bool{membership.RoutingKeyMemberJoined: true, membership.RoutingKeyMemberLeft: false} {
		sync := &fakeServerSync{}
		if err := NewServerEventsHandler(sync).Handle(context.Background(), key, []byte(body)); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if len(sync.memberships) != 1 {
			t.Fatalf("%s: membresías aplicadas: %+v", key, sync.memberships)
		}
		m := sync.memberships[0]
		if m.ServerID != serverID || m.UserID != otherID || m.IsMember != wantMember || !m.OccurredAt.Equal(time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("%s: %+v", key, m)
		}
	}
}

func TestInvalidServerEventsAreDiscardedWithoutTouchingTheStore(t *testing.T) {
	at := `"occurred_at":"2026-09-27T10:00:00Z"`
	cases := []struct {
		name, key, body string
		want            audit.Status
	}{
		{"updated: JSON roto", servers.RoutingKeyServerUpdated, `{no es json`, audit.StatusDiscardedMalformed},
		{"deleted: JSON roto", servers.RoutingKeyServerDeleted, `[]`, audit.StatusDiscardedMalformed},
		{"joined: JSON roto", membership.RoutingKeyMemberJoined, `nope`, audit.StatusDiscardedMalformed},
		{"left: JSON roto", membership.RoutingKeyMemberLeft, `nope`, audit.StatusDiscardedMalformed},
		{"updated: server_id no UUID", servers.RoutingKeyServerUpdated, `{` + at + `,"server_id":"s1","name":"n","owner_id":"` + otherID + `","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"updated: owner_id no UUID", servers.RoutingKeyServerUpdated, `{` + at + `,"server_id":"` + serverID + `","name":"n","owner_id":"x","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"updated: nombre vacío", servers.RoutingKeyServerUpdated, `{` + at + `,"server_id":"` + serverID + `","name":"  ","owner_id":"` + otherID + `","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"updated: sin created_at", servers.RoutingKeyServerUpdated, `{` + at + `,"server_id":"` + serverID + `","name":"n","owner_id":"` + otherID + `"}`, audit.StatusDiscardedInvalid},
		{"updated: sin occurred_at", servers.RoutingKeyServerUpdated, `{"server_id":"` + serverID + `","name":"n","owner_id":"` + otherID + `","created_at":"2026-09-26T14:30:00Z"}`, audit.StatusDiscardedInvalid},
		{"updated: fecha mal formada", servers.RoutingKeyServerUpdated, `{` + at + `,"server_id":"` + serverID + `","name":"n","owner_id":"` + otherID + `","created_at":"ayer"}`, audit.StatusDiscardedMalformed},
		{"deleted: server_id no UUID", servers.RoutingKeyServerDeleted, `{` + at + `,"server_id":"x"}`, audit.StatusDiscardedInvalid},
		{"deleted: sin occurred_at", servers.RoutingKeyServerDeleted, `{"server_id":"` + serverID + `"}`, audit.StatusDiscardedInvalid},
		{"joined: user_id no UUID", membership.RoutingKeyMemberJoined, `{` + at + `,"server_id":"` + serverID + `","user_id":"x"}`, audit.StatusDiscardedInvalid},
		{"left: server_id vacío", membership.RoutingKeyMemberLeft, `{` + at + `,"user_id":"` + otherID + `"}`, audit.StatusDiscardedInvalid},
		{"joined: sin occurred_at", membership.RoutingKeyMemberJoined, `{"server_id":"` + serverID + `","user_id":"` + otherID + `"}`, audit.StatusDiscardedInvalid},
		{"clave desconocida", "servers.otra_cosa", `{}`, audit.StatusDiscardedUnknownKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sync := &fakeServerSync{}
			err := NewServerEventsHandler(sync).Handle(context.Background(), tc.key, []byte(tc.body))
			if got := status(t, err); got != tc.want {
				t.Errorf("status = %s, quería %s", got, tc.want)
			}
			if sync.touched() != 0 {
				t.Error("un evento inválido no debe llegar a la base")
			}
		})
	}
}

// Una falla de base NO es un descarte: se devuelve tal cual para que el
// consumer reencole el mensaje.
func TestServerStoreErrorIsTransientNotDiscarded(t *testing.T) {
	boom := errors.New("db caída")
	body := `{"occurred_at":"2026-09-27T10:00:00Z","server_id":"` + serverID + `","user_id":"` + otherID + `","name":"n","owner_id":"` + otherID + `","created_at":"2026-09-26T14:30:00Z"}`
	for _, key := range ServerBindings {
		if key == reports.RoutingKeyReportUpdated || key == moderation.RoutingKeyModerationActionRecorded {
			continue // tienen otro cuerpo y su propio test (dossier_events_test.go)
		}
		err := NewServerEventsHandler(&fakeServerSync{err: boom}).Handle(context.Background(), key, []byte(body))
		if !errors.Is(err, boom) {
			t.Fatalf("%s: err = %v, quería el error de la base", key, err)
		}
		var d *DiscardError
		if errors.As(err, &d) {
			t.Errorf("%s: un error de base no debe marcarse como descarte", key)
		}
		if !Process(context.Background(), NewServerEventsHandler(&fakeServerSync{err: boom}), &recorderSpy{}, key, []byte(body)) {
			t.Errorf("%s: Process debe pedir reencolar ante un error de base", key)
		}
	}
}

func TestServerUpdatedKeepsTheIcon(t *testing.T) {
	sync := &fakeServerSync{}
	body := `{"event_id":"e1","occurred_at":"2026-09-26T14:30:00Z","server_id":"` + serverID + `","name":"Gamers","owner_id":"` + otherID + `","created_at":"2026-09-26T14:00:00Z","icon_url":"/v1/servers/` + serverID + `/icon?v=abc123"}`
	if err := NewServerEventsHandler(sync).Handle(context.Background(), servers.RoutingKeyServerUpdated, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(sync.snapshots) != 1 || sync.snapshots[0].IconURL != "/v1/servers/"+serverID+"/icon?v=abc123" {
		t.Fatalf("foto aplicada: %+v", sync.snapshots)
	}
}
