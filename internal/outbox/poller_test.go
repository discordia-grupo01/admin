package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore entrega los eventos en orden y marca como publicados los que
// publish acepta, igual que PostgresStore.
type fakeStore struct {
	mu      sync.Mutex
	events  []Event
	err     error
	limits  []int
	handled int
}

func (s *fakeStore) Dispatch(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = append(s.limits, limit)
	if s.err != nil {
		return 0, s.err
	}
	n := 0
	for len(s.events) > 0 {
		if err := publish(ctx, s.events[0]); err != nil {
			break
		}
		s.events = s.events[1:]
		n++
	}
	s.handled += n
	return n, nil
}

type published struct {
	key  string
	body string
}

type fakePublisher struct {
	mu       sync.Mutex
	got      []published
	failWith error
}

func (p *fakePublisher) Publish(_ context.Context, key string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failWith != nil {
		return p.failWith
	}
	raw, ok := payload.(json.RawMessage)
	if !ok {
		return errors.New("el payload debe viajar como json.RawMessage para no re-serializarlo")
	}
	p.got = append(p.got, published{key, string(raw)})
	return nil
}

func TestDrainPublishesInOrder(t *testing.T) {
	store := &fakeStore{events: []Event{
		{ID: "1", RoutingKey: "admin.user_suspended", Payload: []byte(`{"n":1}`)},
		{ID: "2", RoutingKey: "admin.user_reactivated", Payload: []byte(`{"n":2}`)},
	}}
	pub := &fakePublisher{}
	NewPoller(store, pub, time.Second).Drain(context.Background())

	if len(pub.got) != 2 || pub.got[0].body != `{"n":1}` || pub.got[1].key != "admin.user_reactivated" {
		t.Fatalf("publicó %+v", pub.got)
	}
	if store.limits[0] != batchSize {
		t.Errorf("limit = %d, quería %d", store.limits[0], batchSize)
	}
}

func TestDrainLeavesEventPendingWhenBrokerFails(t *testing.T) {
	store := &fakeStore{events: []Event{{ID: "1", RoutingKey: "k", Payload: []byte(`{}`)}}}
	pub := &fakePublisher{failWith: errors.New("broker caído")}
	p := NewPoller(store, pub, time.Second)

	p.Drain(context.Background())
	if len(store.events) != 1 || store.handled != 0 {
		t.Fatalf("el evento debe seguir pendiente: events=%d handled=%d", len(store.events), store.handled)
	}

	// Cuando el broker vuelve, el próximo tick lo publica.
	pub.failWith = nil
	p.Drain(context.Background())
	if len(store.events) != 0 || len(pub.got) != 1 {
		t.Fatalf("tras recuperarse debía publicarse: events=%d got=%d", len(store.events), len(pub.got))
	}
}

func TestDrainSurvivesStoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("db caída")}
	pub := &fakePublisher{}
	NewPoller(store, pub, time.Second).Drain(context.Background()) // no debe entrar en pánico
	if len(pub.got) != 0 {
		t.Errorf("no debería haber publicado nada: %+v", pub.got)
	}
}

func TestStartDrainsPeriodicallyAndStopsOnCancel(t *testing.T) {
	store := &fakeStore{}
	pub := &fakePublisher{}
	p := NewPoller(store, pub, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Start(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for {
		store.mu.Lock()
		ticks := len(store.limits)
		store.mu.Unlock()
		if ticks >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("el poller no corrió periódicamente")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start no terminó al cancelar el contexto")
	}
}
