-- Auditoría de los eventos consumidos: sirve para depurar y para no perder de
-- vista descartes (payload malformado, firma inválida, clave desconocida).
-- La idempotencia real la dan los upserts por versión en cada handler.
CREATE TABLE IF NOT EXISTS consumed_events (
    id           UUID PRIMARY KEY,
    event_id     TEXT,
    routing_key  TEXT NOT NULL,
    raw_body     TEXT NOT NULL,
    status       TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 1,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    error        TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_consumed_events_event_id
    ON consumed_events (event_id) WHERE event_id IS NOT NULL;
