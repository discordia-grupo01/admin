package changes

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/discordia-grupo01/admin/internal/logging"
	"github.com/discordia-grupo01/admin/internal/outbox"
	"github.com/discordia-grupo01/events-contract/backoffice"
	"github.com/discordia-grupo01/events-contract/envelope"
)

const (
	Users   = "users"
	Servers = "servers"
	Reports = "reports"
	Staff   = "staff"
	Dossier = "dossier"
	Metrics = "metrics"

	RecoveryRequests = "recovery_requests"
)

type Notifier interface {
	Changed(ctx context.Context, resources ...string)
}

type Audience interface {
	StaffUserIDs(ctx context.Context) ([]string, error)
}

type Sink interface {
	Save(ctx context.Context, event backoffice.Changed) error
}

type OutboxSink struct {
	db *sql.DB
}

func NewOutboxSink(db *sql.DB) *OutboxSink {
	return &OutboxSink{db: db}
}

func (s *OutboxSink) Save(ctx context.Context, event backoffice.Changed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := outbox.Insert(ctx, tx, backoffice.RoutingKeyChanged, event); err != nil {
		return err
	}
	return tx.Commit()
}

type Publisher struct {
	sink     Sink
	audience Audience
	window   time.Duration

	mu    sync.Mutex
	state map[string]*debounce
}

type debounce struct {
	pending bool
}

func NewPublisher(sink Sink, audience Audience, metricsWindow time.Duration) *Publisher {
	return &Publisher{sink: sink, audience: audience, window: metricsWindow, state: map[string]*debounce{}}
}

func (p *Publisher) Changed(ctx context.Context, resources ...string) {
	for _, resource := range resources {
		if resource == Metrics && p.window > 0 {
			p.debounced(resource)
			continue
		}
		p.publish(ctx, resource)
	}
}

func (p *Publisher) debounced(resource string) {
	p.mu.Lock()
	current, cooling := p.state[resource]
	if cooling {
		current.pending = true
		p.mu.Unlock()
		return
	}
	p.state[resource] = &debounce{}
	p.mu.Unlock()

	p.publish(context.Background(), resource)
	time.AfterFunc(p.window, func() { p.release(resource) })
}

func (p *Publisher) release(resource string) {
	p.mu.Lock()
	current := p.state[resource]
	if current == nil || !current.pending {
		delete(p.state, resource)
		p.mu.Unlock()
		return
	}
	current.pending = false
	p.mu.Unlock()

	p.publish(context.Background(), resource)
	time.AfterFunc(p.window, func() { p.release(resource) })
}

func (p *Publisher) publish(ctx context.Context, resource string) {
	audience, err := p.audience.StaffUserIDs(ctx)
	if err != nil {
		logging.Warnf("changes: no se pudo leer el staff para avisar de %s: %v", resource, err)
		return
	}
	if len(audience) == 0 {
		return
	}
	event := backoffice.Changed{
		Meta:            envelope.Meta{EventID: uuid.NewString(), OccurredAt: time.Now().UTC()},
		Resource:        resource,
		AudienceUserIDs: audience,
	}
	if err := p.sink.Save(ctx, event); err != nil {
		logging.Warnf("changes: no se pudo guardar el aviso de %s: %v", resource, err)
	}
}
