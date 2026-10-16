-- Réplica de servidores para el backoffice. Todo llega por eventos de servers:
-- servers.server_updated (foto completa), servers.member_joined/member_left
-- (para contar miembros) y servers.server_deleted.
CREATE TABLE IF NOT EXISTS servers (
    server_id   UUID PRIMARY KEY,
    -- name, owner_id y created_at son NULL hasta recibir servers.server_updated:
    -- un servidor no aparece en el listado hasta entonces.
    name        TEXT,
    owner_id    UUID,
    created_at  TIMESTAMPTZ,
    -- occurred_at del último server_updated aplicado. Ese evento no trae versión:
    -- gana la foto más nueva, así una repetida o fuera de orden no pisa datos.
    snapshot_at TIMESTAMPTZ,
    -- Marca de borrado (servers.server_deleted). Queda la fila para que un
    -- server_updated tardío no resucite un servidor eliminado.
    deleted_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_servers_created
    ON servers (created_at DESC, server_id) WHERE created_at IS NOT NULL AND deleted_at IS NULL;

-- Membresías para calcular member_count. Una fila por (servidor, usuario) con el
-- último estado conocido; updated_at es el occurred_at del evento que lo fijó,
-- así un member_left atrasado no pisa a un member_joined más nuevo.
CREATE TABLE IF NOT EXISTS server_members (
    server_id  UUID NOT NULL,
    user_id    UUID NOT NULL,
    is_member  BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (server_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_server_members_active
    ON server_members (server_id) WHERE is_member;
