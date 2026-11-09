package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
)

const (
	dossierUser   = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"
	dossierServer = "3b9a6c1e-5d2f-4a8e-b7c4-1f9e3d5b7a2c"
)

type fakeDossierRepo struct {
	user        *model.User
	server      *model.Server
	userErr     error
	serverErr   error
	activity    []repository.ActivityEntry
	lastAct     *time.Time
	memberships []model.DossierMembership
	reports     []model.DossierReport
	moderation  []model.DossierModerationAction
	messaging   []model.MessagingDay
	total       int
	err         error

	gotLimit, gotOffset int
	gotToday            time.Time
	calls               int
}

func (f *fakeDossierRepo) FindUser(context.Context, string) (*model.User, error) {
	f.calls++
	return f.user, f.userErr
}

func (f *fakeDossierRepo) FindServer(context.Context, string) (*model.Server, error) {
	f.calls++
	return f.server, f.serverErr
}

func (f *fakeDossierRepo) UserMemberships(_ context.Context, _ string, limit, offset int) ([]model.DossierMembership, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.memberships, f.total, f.err
}

func (f *fakeDossierRepo) UserActivity(_ context.Context, _ string, limit, offset int) ([]repository.ActivityEntry, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.activity, f.total, f.err
}

func (f *fakeDossierRepo) UserLastActivity(context.Context, string) (*time.Time, error) {
	return f.lastAct, f.err
}

func (f *fakeDossierRepo) UserReports(_ context.Context, _ string, limit, offset int) ([]model.DossierReport, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.reports, f.total, f.err
}

func (f *fakeDossierRepo) UserModeration(_ context.Context, _ string, limit, offset int) ([]model.DossierModerationAction, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.moderation, f.total, f.err
}

func (f *fakeDossierRepo) ServerReports(_ context.Context, _ string, limit, offset int) ([]model.DossierReport, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.reports, f.total, f.err
}

func (f *fakeDossierRepo) ServerModeration(_ context.Context, _ string, limit, offset int) ([]model.DossierModerationAction, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.moderation, f.total, f.err
}

func (f *fakeDossierRepo) ServerMessaging(_ context.Context, _ string, today time.Time) ([]model.MessagingDay, error) {
	f.gotToday = today
	return f.messaging, f.err
}

func newDossier(f *fakeDossierRepo) *DossierService {
	s := NewDossierService(f)
	s.now = func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }
	return s
}

func TestActivityDescriptions(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		entry repository.ActivityEntry
		want  string
	}{
		{repository.ActivityEntry{Kind: "message_sent", ServerName: "GamersHub", ChannelName: "general"}, "Envió un mensaje en #general de GamersHub"},
		{repository.ActivityEntry{Kind: "message_sent", ServerName: "GamersHub"}, "Envió un mensaje en un canal de GamersHub"},
		{repository.ActivityEntry{Kind: "message_sent", ChannelName: "general"}, "Envió un mensaje en #general de un servidor"},
		{repository.ActivityEntry{Kind: "joined_server", ServerName: "GamersHub"}, "Se unió a GamersHub"},
		{repository.ActivityEntry{Kind: "left_server", ServerName: "GamersHub"}, "Salió de GamersHub"},
		{repository.ActivityEntry{Kind: "left_server"}, "Salió de un servidor"},
	}
	for _, tc := range cases {
		tc.entry.OccurredAt = at
		got := describeActivity([]repository.ActivityEntry{tc.entry})
		if len(got) != 1 || got[0].Description != tc.want || got[0].Kind != tc.entry.Kind || !got[0].OccurredAt.Equal(at) {
			t.Errorf("%+v → %+v, quería %q", tc.entry, got, tc.want)
		}
	}
}

func TestUserSummaryCollectsTotalsAndTheLatestFive(t *testing.T) {
	last := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	f := &fakeDossierRepo{
		user:     &model.User{ID: dossierUser, Name: "Ale"},
		lastAct:  &last,
		total:    12,
		activity: []repository.ActivityEntry{{ID: "a1", Kind: "joined_server", ServerName: "Sala"}},
	}
	got, err := newDossier(f).UserSummary(context.Background(), "7C1E5A3B-9D2F-4B8E-A6C4-3F1E9D7B5A2C")
	if err != nil {
		t.Fatal(err)
	}
	if got.User.Name != "Ale" || got.LastActivityAt == nil || !got.LastActivityAt.Equal(last) {
		t.Errorf("resumen = %+v", got)
	}
	if c := got.Counts; c.Servers != 12 || c.Activity != 12 || c.Reports != 12 || c.Moderation != 12 {
		t.Errorf("totales = %+v", c)
	}
	if len(got.Latest.Activity) != 1 || got.Latest.Activity[0].Description != "Se unió a Sala" {
		t.Errorf("actividad = %+v", got.Latest.Activity)
	}
	if f.gotLimit != model.DossierPreviewSize || f.gotOffset != 0 {
		t.Errorf("el resumen pide %d ítems desde %d, quería %d desde 0", f.gotLimit, f.gotOffset, model.DossierPreviewSize)
	}
}

func TestUserSummaryErrors(t *testing.T) {
	boom := errors.New("db caída")
	cases := map[string]struct {
		repo *fakeDossierRepo
		id   string
		want error
	}{
		"id que no es UUID":   {&fakeDossierRepo{}, "no-es-uuid", ErrUserNotFound},
		"usuario inexistente": {&fakeDossierRepo{userErr: repository.ErrUserNotFound}, dossierUser, ErrUserNotFound},
		"falla de base":       {&fakeDossierRepo{user: &model.User{}, err: boom}, dossierUser, boom},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newDossier(tc.repo).UserSummary(context.Background(), tc.id); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, quería %v", err, tc.want)
			}
		})
	}
}

func TestUserSectionNormalizesTheLimitAndReturnsAPage(t *testing.T) {
	f := &fakeDossierRepo{user: &model.User{}, total: 3, reports: []model.DossierReport{{ID: "r1"}}}
	got, err := newDossier(f).UserSection(context.Background(), dossierUser, "reports", 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := got.(model.DossierPage[model.DossierReport])
	if !ok || p.Total != 3 || p.Limit != model.DefaultLimit || p.Offset != 40 || len(p.Items) != 1 {
		t.Errorf("página = %#v", got)
	}
	if f.gotLimit != model.DefaultLimit || f.gotOffset != 40 {
		t.Errorf("repo recibió limit=%d offset=%d", f.gotLimit, f.gotOffset)
	}

	if got, _ := newDossier(f).UserSection(context.Background(), dossierUser, "reports", 500, 0); got.(model.DossierPage[model.DossierReport]).Limit != model.MaxLimit {
		t.Error("el límite tiene que acotarse a 100")
	}
}

func TestUserSectionReturnsEmptyItemsNeverNull(t *testing.T) {
	f := &fakeDossierRepo{user: &model.User{}}
	for _, section := range []string{"servers", "activity", "reports", "moderation"} {
		got, err := newDossier(f).UserSection(context.Background(), dossierUser, section, 10, 0)
		if err != nil {
			t.Fatalf("%s: %v", section, err)
		}
		switch p := got.(type) {
		case model.DossierPage[model.DossierMembership]:
			if p.Items == nil {
				t.Errorf("%s: items null", section)
			}
		case model.DossierPage[model.DossierActivity]:
			if p.Items == nil {
				t.Errorf("%s: items null", section)
			}
		case model.DossierPage[model.DossierReport]:
			if p.Items == nil {
				t.Errorf("%s: items null", section)
			}
		case model.DossierPage[model.DossierModerationAction]:
			if p.Items == nil {
				t.Errorf("%s: items null", section)
			}
		default:
			t.Errorf("%s: tipo inesperado %T", section, got)
		}
	}
}

func TestInvalidSectionsAreRejectedBeforeQuerying(t *testing.T) {
	f := &fakeDossierRepo{user: &model.User{}, server: &model.Server{}}
	svc := newDossier(f)
	if _, err := svc.UserSection(context.Background(), dossierUser, "logins", 10, 0); !errors.Is(err, ErrInvalidSection) {
		t.Errorf("sección de usuario: err = %v", err)
	}
	// "servers" y "activity" existen para usuarios pero no para servidores.
	for _, section := range []string{"servers", "activity", "otra"} {
		if _, err := svc.ServerSection(context.Background(), dossierServer, section, 10, 0); !errors.Is(err, ErrInvalidSection) {
			t.Errorf("sección de servidor %q: err = %v", section, err)
		}
	}
	if f.calls != 0 {
		t.Errorf("no tenía que consultar la base (%d consultas)", f.calls)
	}
}

func TestServerSummaryAndSection(t *testing.T) {
	f := &fakeDossierRepo{
		server:    &model.Server{ID: dossierServer, Name: "Sala"},
		messaging: []model.MessagingDay{{Date: "2026-11-08", Messages: 4}},
		total:     2,
	}
	svc := newDossier(f)
	got, err := svc.ServerSummary(context.Background(), dossierServer)
	if err != nil {
		t.Fatal(err)
	}
	if got.Server.Name != "Sala" || len(got.Messaging) != 1 || got.Counts.Reports != 2 || got.Counts.Moderation != 2 {
		t.Errorf("resumen = %+v", got)
	}
	if want := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC); !f.gotToday.Equal(want) {
		t.Errorf("hoy = %v, quería %v", f.gotToday, want)
	}

	page, err := svc.ServerSection(context.Background(), dossierServer, "moderation", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := page.(model.DossierPage[model.DossierModerationAction]); !ok || p.Total != 2 {
		t.Errorf("página = %#v", page)
	}

	if _, err := svc.ServerSummary(context.Background(), "x"); !errors.Is(err, ErrServerNotFound) {
		t.Errorf("id inválido: err = %v", err)
	}
	f.serverErr = repository.ErrServerNotFound
	if _, err := svc.ServerSection(context.Background(), dossierServer, "reports", 10, 0); !errors.Is(err, ErrServerNotFound) {
		t.Errorf("servidor inexistente: err = %v", err)
	}
}
