-- Réplica de usuarios para el backoffice. Los datos de la cuenta (email,
-- nombre, fecha de registro) llegan por eventos de identify; el estado de
-- suspensión es de admin (admin es la fuente de verdad de quién está suspendido).
--
-- Sin pg_trgm: la búsqueda parcial usa ILIKE con scan secuencial, que alcanza
-- a esta escala y evita depender de una extensión que Supabase puede restringir.
CREATE TABLE IF NOT EXISTS users (
    user_id            UUID PRIMARY KEY,
    name               TEXT,
    email              TEXT,
    -- NULL hasta recibir users.account_registered: un usuario no aparece en el
    -- listado hasta entonces (puede llegar antes users.profile_updated).
    registered_at      TIMESTAMPTZ,
    -- Última profile_version aplicada. 0 = el nombre vino del registro; todo
    -- users.profile_updated (version >= 1) lo pisa.
    profile_version    BIGINT NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    -- Sube con cada suspensión o reactivación y ordena los eventos admin.*.
    suspension_version BIGINT NOT NULL DEFAULT 0,
    suspended_at       TIMESTAMPTZ,
    suspended_by       UUID,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_suspension_consistent CHECK ((status = 'suspended') = (suspended_at IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_users_registered
    ON users (registered_at DESC, user_id) WHERE registered_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_status ON users (status);
