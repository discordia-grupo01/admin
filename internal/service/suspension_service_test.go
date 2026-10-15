package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/model"
	"github.com/discordia-grupo01/admin/internal/repository"
	"github.com/discordia-grupo01/events-contract/admin"
)

const (
	testUserID  = "7c1e5a3b-9d2f-4b8e-a6c4-3f1e9d7b5a2c"
	testStaffID = "11111111-1111-1111-1111-111111111111"
	testSecret  = "secreto-de-prueba"
)

type fakeSuspensionRepo struct {
	err error

	suspended []repository.SuspendedUser

	called  bool
	userID  string
	staffID string
	at      time.Time
	key     string
	payload any
}

func (f *fakeSuspensionRepo) Suspend(_ context.Context, userID, staffID string, at time.Time, event repository.EventBuilder) (*model.User, error) {
	f.called = true
	f.userID, f.staffID, f.at = userID, staffID, at
	if f.err != nil {
		return nil, f.err
	}
	f.key, f.payload = event(4)
	return &model.User{ID: userID, Status: model.UserStatusSuspended, SuspendedAt: &at}, nil
}

func (f *fakeSuspensionRepo) Reactivate(_ context.Context, userID string, event repository.EventBuilder) (*model.User, error) {
	f.called = true
	f.userID = userID
	if f.err != nil {
		return nil, f.err
	}
	f.key, f.payload = event(5)
	return &model.User{ID: userID, Status: model.UserStatusActive}, nil
}

func (f *fakeSuspensionRepo) ListSuspended(context.Context) ([]repository.SuspendedUser, error) {
	return f.suspended, f.err
}

func newSuspensionService(repo repository.SuspensionRepository) *SuspensionService {
	s := NewSuspensionService(repo, testSecret)
	s.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 123, time.FixedZone("ART", -3*3600)) }
	s.newEventID = func() string { return "evento-1" }
	return s
}

func TestSuspendBuildsASignedEvent(t *testing.T) {
	repo := &fakeSuspensionRepo{}
	u, err := newSuspensionService(repo).Suspend(context.Background(), "7C1E5A3B-9D2F-4B8E-A6C4-3F1E9D7B5A2C", testStaffID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != model.UserStatusSuspended || repo.userID != testUserID || repo.staffID != testStaffID {
		t.Errorf("usuario=%+v repo: user=%q staff=%q", u, repo.userID, repo.staffID)
	}
	if repo.at.Location() != time.UTC {
		t.Errorf("la hora debe pasarse en UTC: %v", repo.at)
	}
	if repo.at.Nanosecond()%1000 != 0 {
		t.Errorf("la hora debe truncarse a microsegundos (precisión de Postgres): %v", repo.at)
	}

	if repo.key != admin.RoutingKeyUserSuspended {
		t.Fatalf("routing key = %q", repo.key)
	}
	// Lo que viaja es lo que se serializa al outbox: se verifica tras el viaje por JSON.
	raw, err := json.Marshal(repo.payload)
	if err != nil {
		t.Fatal(err)
	}
	var got admin.UserSuspended
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.EventID != "evento-1" || got.UserID != testUserID || got.Version != 4 || !got.OccurredAt.Equal(repo.at) {
		t.Errorf("evento: %+v", got)
	}
	if !got.Verify(testSecret) {
		t.Error("la firma no verifica con el secreto de admin")
	}
	if got.Verify("otro-secreto") {
		t.Error("la firma no debe verificar con otro secreto")
	}
	// No se puede reusar como reactivación.
	if (admin.UserReactivated{Meta: got.Meta, UserID: got.UserID, Version: got.Version, Signature: got.Signature}).Verify(testSecret) {
		t.Error("una firma de suspensión no debe valer como reactivación")
	}
}

func TestSuspendTreatsAnInvalidIDAsNotFoundWithoutQuerying(t *testing.T) {
	repo := &fakeSuspensionRepo{}
	for _, id := range []string{"", "abc", "123", "7c1e5a3b-9d2f-4b8e-a6c4"} {
		if _, err := newSuspensionService(repo).Suspend(context.Background(), id, testStaffID); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("id %q: err = %v", id, err)
		}
	}
	if repo.called {
		t.Error("un id inválido no debe llegar a la base")
	}
}

func TestSuspendPropagatesBusinessErrors(t *testing.T) {
	for _, want := range []error{ErrUserNotFound, ErrCannotSuspendStaff, ErrUserAlreadySuspended} {
		_, err := newSuspensionService(&fakeSuspensionRepo{err: want}).Suspend(context.Background(), testUserID, testStaffID)
		if !errors.Is(err, want) {
			t.Errorf("err = %v, quería %v", err, want)
		}
	}
	boom := errors.New("db caída")
	if _, err := newSuspensionService(&fakeSuspensionRepo{err: boom}).Suspend(context.Background(), testUserID, testStaffID); !errors.Is(err, boom) {
		t.Errorf("err = %v, quería el error de la base envuelto", err)
	}
}

func TestReactivateBuildsASignedEventWithTheNewVersion(t *testing.T) {
	repo := &fakeSuspensionRepo{}
	u, err := newSuspensionService(repo).Reactivate(context.Background(), "7C1E5A3B-9D2F-4B8E-A6C4-3F1E9D7B5A2C")
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != model.UserStatusActive || repo.userID != testUserID {
		t.Errorf("usuario=%+v repo.userID=%q", u, repo.userID)
	}
	if repo.key != admin.RoutingKeyUserReactivated {
		t.Fatalf("routing key = %q", repo.key)
	}
	raw, err := json.Marshal(repo.payload)
	if err != nil {
		t.Fatal(err)
	}
	var got admin.UserReactivated
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.EventID != "evento-1" || got.UserID != testUserID || got.Version != 5 {
		t.Errorf("evento: %+v", got)
	}
	if !got.Verify(testSecret) || got.Verify("otro-secreto") {
		t.Error("la firma debe verificar solo con el secreto de admin")
	}
	// No se puede reusar como suspensión.
	if (admin.UserSuspended{Meta: got.Meta, UserID: got.UserID, Version: got.Version, Signature: got.Signature}).Verify(testSecret) {
		t.Error("una firma de reactivación no debe valer como suspensión")
	}
}

func TestReactivateTreatsAnInvalidIDAsNotFoundWithoutQuerying(t *testing.T) {
	repo := &fakeSuspensionRepo{}
	if _, err := newSuspensionService(repo).Reactivate(context.Background(), "abc"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("err = %v", err)
	}
	if repo.called {
		t.Error("un id inválido no debe llegar a la base")
	}
}

func TestReactivatePropagatesBusinessErrors(t *testing.T) {
	for _, want := range []error{ErrUserNotFound, ErrUserNotSuspended} {
		if _, err := newSuspensionService(&fakeSuspensionRepo{err: want}).Reactivate(context.Background(), testUserID); !errors.Is(err, want) {
			t.Errorf("err = %v, quería %v", err, want)
		}
	}
}

type fakePublisher struct {
	keys     []string
	payloads []any
	failAt   int // 1-based; 0 = nunca falla
	err      error
}

func (f *fakePublisher) Publish(_ context.Context, key string, payload any) error {
	if f.failAt > 0 && len(f.keys)+1 == f.failAt {
		return f.err
	}
	f.keys = append(f.keys, key)
	f.payloads = append(f.payloads, payload)
	return nil
}

func TestRepublishSuspensionsPublishesOneSignedEventPerSuspendedUser(t *testing.T) {
	first := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	second := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	repo := &fakeSuspensionRepo{suspended: []repository.SuspendedUser{
		{UserID: testUserID, Version: 1, SuspendedAt: first},
		{UserID: testStaffID, Version: 3, SuspendedAt: second},
	}}
	pub := &fakePublisher{}

	n, err := newSuspensionService(repo).RepublishSuspensions(context.Background(), pub)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for i, want := range repo.suspended {
		if pub.keys[i] != admin.RoutingKeyUserSuspended {
			t.Errorf("routing key %d = %q", i, pub.keys[i])
		}
		e, ok := pub.payloads[i].(admin.UserSuspended)
		if !ok {
			t.Fatalf("payload %d: %T", i, pub.payloads[i])
		}
		if e.UserID != want.UserID || e.Version != want.Version || !e.OccurredAt.Equal(want.SuspendedAt) || !e.Verify(testSecret) {
			t.Errorf("evento %d: %+v", i, e)
		}
	}
}

func TestRepublishSuspensionsWithNobodySuspendedPublishesNothing(t *testing.T) {
	pub := &fakePublisher{}
	n, err := newSuspensionService(&fakeSuspensionRepo{}).RepublishSuspensions(context.Background(), pub)
	if err != nil || n != 0 || len(pub.keys) != 0 {
		t.Errorf("n=%d err=%v publicados=%d", n, err, len(pub.keys))
	}
}

func TestRepublishSuspensionsStopsAtTheFirstPublishError(t *testing.T) {
	repo := &fakeSuspensionRepo{suspended: []repository.SuspendedUser{{UserID: "u1", Version: 1}, {UserID: "u2", Version: 1}, {UserID: "u3", Version: 1}}}
	boom := errors.New("broker caído")
	pub := &fakePublisher{failAt: 2, err: boom}

	n, err := newSuspensionService(repo).RepublishSuspensions(context.Background(), pub)
	if !errors.Is(err, boom) || n != 1 || len(pub.keys) != 1 {
		t.Errorf("n=%d err=%v publicados=%d, quería cortar tras 1", n, err, len(pub.keys))
	}
}

func TestRepublishSuspensionsPropagatesListError(t *testing.T) {
	boom := errors.New("db caída")
	if _, err := newSuspensionService(&fakeSuspensionRepo{err: boom}).RepublishSuspensions(context.Background(), &fakePublisher{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
