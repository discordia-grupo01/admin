-- Outbox transaccional: cada cambio de negocio inserta acá su evento en la
-- misma transacción, y un poller lo publica en RabbitMQ después. Un corte del
-- broker o un crash entre el commit y el publish demora el evento, no lo pierde.
CREATE TABLE IF NOT EXISTS outbox_events (
    id           UUID PRIMARY KEY,
    routing_key  TEXT NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_pending
    ON outbox_events (created_at) WHERE published_at IS NULL;
