-- Eliminación de servidores por el staff (HU #68). Admin no borra el servidor:
-- registra el pedido y publica admin.server_deletion_requested; el servidor
-- sale del listado recién cuando servers confirma con servers.server_deleted
-- (deleted_at), así un pedido perdido no deja un servidor vivo e invisible.
--
-- moderation_version sube con cada acción de moderación sobre el servidor y es
-- la `version` del evento: los consumidores aplican solo versiones mayores.
-- deletion_requested_* guardan el último pedido y quién lo hizo, solo para
-- trazabilidad (el evento no lleva al staff).
ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS moderation_version    BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS deletion_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deletion_requested_by UUID;
