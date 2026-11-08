package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/discordia-grupo01/admin/internal/repository"
)

type fakeActivityRepo struct {
	mu      sync.Mutex
	cutoffs []time.Time
	deleted int64
	err     error
}

func (f *fakeActivityRepo) InsertMessage(context.Context, repository.MessageActivity) error {
	return nil
}

func (f *fakeActivityRepo) Prune(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, before)
	return f.deleted, f.err
}

func (f *fakeActivityRepo) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cutoffs)
}

func TestPruneDeletesWhatIsOlderThanTheRetention(t *testing.T) {
	f := &fakeActivityRepo{deleted: 4}
	p := NewActivityPruner(f, 90, time.Hour)
	p.now = func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }

	if n := p.Prune(context.Background()); n != 4 {
		t.Errorf("borradas = %d, quería 4", n)
	}
	if want := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC); len(f.cutoffs) != 1 || !f.cutoffs[0].Equal(want) {
		t.Errorf("corte = %v, quería %v (90 días antes)", f.cutoffs, want)
	}
}

func TestPruneFailureIsLoggedNotFatal(t *testing.T) {
	f := &fakeActivityRepo{err: errors.New("db caída")}
	p := NewActivityPruner(f, 30, time.Hour)
	if n := p.Prune(context.Background()); n != 0 {
		t.Errorf("borradas = %d, quería 0 ante un error", n)
	}
}

func TestStartPrunesAtStartupAndOnEveryTickUntilCancelled(t *testing.T) {
	f := &fakeActivityRepo{}
	p := NewActivityPruner(f, 90, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Start(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for f.calls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("solo hubo %d podas: tenía que podar al arrancar y en cada tick", f.calls())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start no terminó al cancelar el contexto")
	}
}
