package changes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/events-contract/backoffice"
	"github.com/discordia-grupo01/events-contract/validate"
)

type fakeSink struct {
	mu     sync.Mutex
	saved  []backoffice.Changed
	failed error
}

func (f *fakeSink) Save(_ context.Context, event backoffice.Changed) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return f.failed
	}
	f.saved = append(f.saved, event)
	return nil
}

func (f *fakeSink) resources() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.saved))
	for i, e := range f.saved {
		out[i] = e.Resource
	}
	return out
}

type fakeAudience struct {
	ids []string
	err error
}

func (a fakeAudience) StaffUserIDs(context.Context) ([]string, error) { return a.ids, a.err }

func TestChangedTellsEveryStaffMemberWhatChanged(t *testing.T) {
	sink := &fakeSink{}
	p := NewPublisher(sink, fakeAudience{ids: []string{"a", "b"}}, 0)

	p.Changed(context.Background(), Users, Dossier)

	if got := sink.resources(); len(got) != 2 || got[0] != Users || got[1] != Dossier {
		t.Fatalf("saved = %v, want users and dossier", got)
	}
	e := sink.saved[0]
	if len(e.AudienceUserIDs) != 2 || e.AudienceUserIDs[0] != "a" || e.EventID == "" || e.OccurredAt.IsZero() {
		t.Errorf("event = %+v", e)
	}
}

func TestChangedMeetsTheEventContract(t *testing.T) {
	sink := &fakeSink{}
	NewPublisher(sink, fakeAudience{ids: []string{"0b6f1c8e-7a2d-4e3f-9c1b-5d4a3e2f1a0b"}}, 0).Changed(context.Background(), Servers)

	body := []byte(`{"event_id":"` + sink.saved[0].EventID + `","occurred_at":"` + sink.saved[0].OccurredAt.Format(time.RFC3339) + `","resource":"servers","audience_user_ids":["u"]}`)
	if err := validate.Validate(backoffice.RoutingKeyChanged, body); err != nil {
		t.Errorf("the event does not meet the contract: %v", err)
	}
}

func TestChangedTellsNobodyWhenThereIsNoStaff(t *testing.T) {
	sink := &fakeSink{}
	NewPublisher(sink, fakeAudience{}, 0).Changed(context.Background(), Users)

	if len(sink.saved) != 0 {
		t.Errorf("saved = %v, want nothing", sink.resources())
	}
}

func TestAFailureNeverBreaksTheCaller(t *testing.T) {
	NewPublisher(&fakeSink{failed: errors.New("db down")}, fakeAudience{ids: []string{"a"}}, 0).Changed(context.Background(), Users)
	NewPublisher(&fakeSink{}, fakeAudience{err: errors.New("db down")}, 0).Changed(context.Background(), Users)
}

func TestMetricsAreToldOnceAndThenOnceMoreWhenThingsKeepHappening(t *testing.T) {
	sink := &fakeSink{}
	window := 40 * time.Millisecond
	p := NewPublisher(sink, fakeAudience{ids: []string{"a"}}, window)

	for i := 0; i < 20; i++ {
		p.Changed(context.Background(), Metrics)
	}
	if got := len(sink.resources()); got != 1 {
		t.Fatalf("right away = %d events, want 1", got)
	}

	time.Sleep(window * 3)
	if got := len(sink.resources()); got != 2 {
		t.Fatalf("after the window = %d events, want 2 (the first and one for what came in between)", got)
	}

	time.Sleep(window * 3)
	if got := len(sink.resources()); got != 2 {
		t.Errorf("when things stop it keeps telling: %d events, want 2", got)
	}

	p.Changed(context.Background(), Metrics)
	if got := len(sink.resources()); got != 3 {
		t.Errorf("after quiet it must tell right away: %d events, want 3", got)
	}
}

func TestOtherResourcesAreNeverDelayed(t *testing.T) {
	sink := &fakeSink{}
	p := NewPublisher(sink, fakeAudience{ids: []string{"a"}}, time.Hour)

	p.Changed(context.Background(), Users)
	p.Changed(context.Background(), Users)

	if got := len(sink.resources()); got != 2 {
		t.Errorf("events = %d, want 2", got)
	}
}

type fakeInner struct {
	err   error
	calls int
}

func (f *fakeInner) Handle(context.Context, string, []byte) error {
	f.calls++
	return f.err
}

type recordedNotifier struct{ calls [][]string }

func (r *recordedNotifier) Changed(_ context.Context, resources ...string) {
	r.calls = append(r.calls, resources)
}

func TestNotifyingTellsAfterTheEventWasApplied(t *testing.T) {
	cases := map[string][]string{
		"users.account_registered":           {Users, Metrics},
		"users.profile_updated":              {Users},
		"users.email_verified":               {Users},
		"servers.server_updated":             {Servers},
		"servers.server_deleted":             {Servers, Metrics},
		"servers.member_joined":              {Servers, Metrics},
		"servers.member_left":                {Servers, Metrics},
		"servers.report_updated":             {Reports, Dossier},
		"servers.moderation_action_recorded": {Dossier},
		"messaging.message_created":          {Metrics},
	}
	for key, want := range cases {
		inner, n := &fakeInner{}, &recordedNotifier{}
		if err := Notifying(inner, n).Handle(context.Background(), key, nil); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if len(n.calls) != 1 || len(n.calls[0]) != len(want) {
			t.Errorf("%s: told %v, want %v", key, n.calls, want)
			continue
		}
		for i := range want {
			if n.calls[0][i] != want[i] {
				t.Errorf("%s: told %v, want %v", key, n.calls[0], want)
			}
		}
	}
}

func TestNotifyingTellsNothingWhenTheEventFailedOrIsUnknown(t *testing.T) {
	failing, n := &fakeInner{err: errors.New("db down")}, &recordedNotifier{}
	if err := Notifying(failing, n).Handle(context.Background(), "users.profile_updated", nil); err == nil {
		t.Fatal("the error must reach the consumer so the event is retried")
	}
	if len(n.calls) != 0 {
		t.Errorf("told %v after a failure", n.calls)
	}

	other := &recordedNotifier{}
	_ = Notifying(&fakeInner{}, other).Handle(context.Background(), "otra.cosa", nil)
	if len(other.calls) != 0 {
		t.Errorf("told %v for an unknown event", other.calls)
	}
}
