-- Historial de acciones de moderación para el expediente (HU #81). Mezcla dos
-- orígenes: las acciones del staff de la plataforma (scope = 'platform'), que
-- admin escribe en la misma transacción que la acción, y las de los
-- moderadores de cada servidor (scope = 'server'), que llegan por
-- servers.moderation_action_recorded.
--
-- Sin claves foráneas a users ni a servers: los eventos pueden llegar antes
-- que la entidad. actor_id solo existe en las acciones de plataforma y no se
-- expone por la API (minimización de datos).
CREATE TABLE IF NOT EXISTS moderation_actions (
    id             UUID PRIMARY KEY,
    scope          TEXT NOT NULL CHECK (scope IN ('platform', 'server')),
    kind           TEXT NOT NULL,
    target_user_id UUID,
    server_id      UUID,
    actor_id       UUID,
    occurred_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_moderation_actions_target
    ON moderation_actions (target_user_id, occurred_at DESC, id) WHERE target_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_moderation_actions_server
    ON moderation_actions (server_id, occurred_at DESC, id) WHERE server_id IS NOT NULL;
