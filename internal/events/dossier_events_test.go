package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/audit"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/moderation"
	"github.com/discordia-grupo01/events-contract/reports"
)

const (
	reportID = "7c1d2e3f-4a5b-4c6d-8e7f-0a1b2c3d4e5f"
	userID   = "5e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f7a8b9"
)

type fakeDossierSync struct {
	reports []repository.ReportSnapshot
	actions []repository.ServerModerationAction
	err     error
}

func (f *fakeDossierSync) ApplyReportUpdated(_ context.Context, r repository.ReportSnapshot) error {
	f.reports = append(f.reports, r)
	return f.err
}

func (f *fakeDossierSync) ApplyModerationActionRecorded(_ context.Context, a repository.ServerModerationAction) error {
	f.actions = append(f.actions, a)
	return f.err
}

func handlerWith(sync *fakeDossierSync) *ServerEventsHandler {
	return NewServerEventsHandler(&fakeServerSync{}).WithDossier(sync)
}

func assertDiscarded(t *testing.T, err error, want audit.Status) {
	t.Helper()
	var d *DiscardError
	if !errors.As(err, &d) || d.Status != want {
		t.Fatalf("err = %v, quería un descarte %s", err, want)
	}
}

func TestReportUpdatedIsApplied(t *testing.T) {
	sync := &fakeDossierSync{}
	body := `{"event_id":"e1","occurred_at":"2026-11-02T10:05:00Z","report_id":"` + reportID + `","server_id":"` + serverID +
		`","kind":"message","reported_user_id":null,"channel_id":"` + otherID + `","message_id":"` + userID +
		`","reason":"spam","status":"resolved","created_at":"2026-11-02T10:00:00Z","resolved_at":"2026-11-02T10:05:00-03:00"}`
	if err := handlerWith(sync).Handle(context.Background(), reports.RoutingKeyReportUpdated, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(sync.reports) != 1 {
		t.Fatalf("aplicado = %+v", sync.reports)
	}
	got := sync.reports[0]
	resolved := time.Date(2026, 11, 2, 13, 5, 0, 0, time.UTC)
	if got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolved) {
		t.Errorf("resolved_at = %v, quería %v", got.ResolvedAt, resolved)
	}
	got.ResolvedAt = nil
	want := repository.ReportSnapshot{
		ReportID: reportID, ServerID: serverID, Kind: "message", ChannelID: otherID, MessageID: userID,
		Reason: "spam", Status: "resolved",
		CreatedAt:  time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		OccurredAt: time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC),
	}
	if got != want {
		t.Errorf("reporte = %+v, quería %+v", got, want)
	}
}

func TestReportUpdatedOfAUserKeepsTheReportedUser(t *testing.T) {
	sync := &fakeDossierSync{}
	body := `{"event_id":"e1","occurred_at":"2026-11-02T10:00:00Z","report_id":"` + reportID + `","server_id":"` + serverID +
		`","kind":"user","reported_user_id":"` + userID + `","channel_id":null,"message_id":null,"reason":"insultos","status":"pending","created_at":"2026-11-02T10:00:00Z","resolved_at":null}`
	if err := handlerWith(sync).Handle(context.Background(), reports.RoutingKeyReportUpdated, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if got := sync.reports[0]; got.ReportedUserID != userID || got.ChannelID != "" || got.MessageID != "" || got.ResolvedAt != nil {
		t.Errorf("reporte = %+v", got)
	}
}

func TestInvalidReportsAreDiscarded(t *testing.T) {
	valid := map[string]string{
		"report_id": `"` + reportID + `"`, "server_id": `"` + serverID + `"`, "kind": `"user"`, "status": `"pending"`,
		"reported_user_id": `"` + userID + `"`, "created_at": `"2026-11-02T10:00:00Z"`, "occurred_at": `"2026-11-02T10:00:00Z"`,
	}
	build := func(over map[string]string) []byte {
		b := `{"event_id":"e1","reason":"x"`
		for k, v := range valid {
			if o, ok := over[k]; ok {
				v = o
			}
			b += `,"` + k + `":` + v
		}
		return []byte(b + `}`)
	}
	cases := map[string]map[string]string{
		"report_id inválido":  {"report_id": `"no"`},
		"server_id inválido":  {"server_id": `"no"`},
		"kind desconocido":    {"kind": `"otro"`},
		"status desconocido":  {"status": `"otro"`},
		"usuario no es UUID":  {"reported_user_id": `"no"`},
		"created_at ausente":  {"created_at": `"0001-01-01T00:00:00Z"`},
		"occurred_at ausente": {"occurred_at": `"0001-01-01T00:00:00Z"`},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			sync := &fakeDossierSync{}
			err := handlerWith(sync).Handle(context.Background(), reports.RoutingKeyReportUpdated, build(over))
			assertDiscarded(t, err, audit.StatusDiscardedInvalid)
			if len(sync.reports) != 0 {
				t.Error("no debió aplicar nada")
			}
		})
	}
	err := handlerWith(&fakeDossierSync{}).Handle(context.Background(), reports.RoutingKeyReportUpdated, []byte(`{`))
	assertDiscarded(t, err, audit.StatusDiscardedMalformed)
}

func TestModerationActionRecordedIsApplied(t *testing.T) {
	for _, action := range []string{"ban", "unban", "kick", "mute", "unmute"} {
		sync := &fakeDossierSync{}
		body := `{"event_id":"e1","occurred_at":"2026-11-02T10:00:00Z","action_id":"` + reportID + `","server_id":"` + serverID +
			`","action":"` + action + `","target_user_id":"` + userID + `"}`
		if err := handlerWith(sync).Handle(context.Background(), moderation.RoutingKeyModerationActionRecorded, []byte(body)); err != nil {
			t.Fatal(err)
		}
		want := repository.ServerModerationAction{
			ActionID: reportID, ServerID: serverID, Kind: action, TargetUserID: userID,
			OccurredAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		}
		if len(sync.actions) != 1 || sync.actions[0] != want {
			t.Errorf("%s: aplicado = %+v", action, sync.actions)
		}
	}
}

func TestInvalidModerationActionsAreDiscarded(t *testing.T) {
	ok := `"event_id":"e1","occurred_at":"2026-11-02T10:00:00Z","action_id":"` + reportID + `","server_id":"` + serverID + `","target_user_id":"` + userID + `"`
	cases := map[string]string{
		"acción desconocida": `{` + ok + `,"action":"banish"}`,
		"sin acción":         `{` + ok + `}`,
		"action_id inválido": `{"event_id":"e1","occurred_at":"2026-11-02T10:00:00Z","action_id":"x","server_id":"` + serverID + `","target_user_id":"` + userID + `","action":"ban"}`,
		"sin occurred_at":    `{"event_id":"e1","action_id":"` + reportID + `","server_id":"` + serverID + `","target_user_id":"` + userID + `","action":"ban"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			sync := &fakeDossierSync{}
			err := handlerWith(sync).Handle(context.Background(), moderation.RoutingKeyModerationActionRecorded, []byte(body))
			assertDiscarded(t, err, audit.StatusDiscardedInvalid)
			if len(sync.actions) != 0 {
				t.Error("no debió aplicar nada")
			}
		})
	}
	err := handlerWith(&fakeDossierSync{}).Handle(context.Background(), moderation.RoutingKeyModerationActionRecorded, []byte(`{`))
	assertDiscarded(t, err, audit.StatusDiscardedMalformed)
}

func TestDatabaseErrorsOfTheDossierEventsAreTransient(t *testing.T) {
	boom := errors.New("db caída")
	sync := &fakeDossierSync{err: boom}
	body := `{"event_id":"e1","occurred_at":"2026-11-02T10:00:00Z","action_id":"` + reportID + `","server_id":"` + serverID + `","action":"ban","target_user_id":"` + userID + `"}`
	err := handlerWith(sync).Handle(context.Background(), moderation.RoutingKeyModerationActionRecorded, []byte(body))
	var d *DiscardError
	if !errors.Is(err, boom) || errors.As(err, &d) {
		t.Errorf("err = %v, quería el error de base sin descartar", err)
	}
}

func TestDossierEventsAreUnknownWithoutADossierSync(t *testing.T) {
	h := NewServerEventsHandler(&fakeServerSync{})
	err := h.Handle(context.Background(), reports.RoutingKeyReportUpdated, []byte(`{}`))
	assertDiscarded(t, err, audit.StatusDiscardedUnknownKey)
}

func TestServerBindingsIncludeTheDossierEvents(t *testing.T) {
	have := map[string]bool{}
	for _, k := range ServerBindings {
		have[k] = true
	}
	for _, k := range []string{reports.RoutingKeyReportUpdated, moderation.RoutingKeyModerationActionRecorded} {
		if !have[k] {
			t.Errorf("falta el binding %s", k)
		}
	}
}
