-- Suspensión de servidores por el staff (HU #69). El servidor conserva todo su
-- contenido, pero nadie puede usarlo mientras suspended_at tenga valor. Admin es
-- la fuente de verdad de quién está suspendido: servers, messaging y voz lo
-- aplican al procesar admin.server_suspended / admin.server_reactivated.
--
-- No hay columna `status`: el estado se deriva de suspended_at, así no hay dos
-- fuentes de verdad. La versión de los eventos es moderation_version (000007),
-- compartida con los pedidos de eliminación.
ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS suspended_by UUID;

-- Quién suspendió solo tiene sentido mientras hay una suspensión vigente.
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_suspension_consistent;
ALTER TABLE servers
    ADD CONSTRAINT servers_suspension_consistent CHECK (suspended_by IS NULL OR suspended_at IS NOT NULL);

-- Para republishsuspensions y para el filtro status=suspended del listado.
CREATE INDEX IF NOT EXISTS idx_servers_suspended
    ON servers (suspended_at, server_id) WHERE suspended_at IS NOT NULL AND deleted_at IS NULL;
