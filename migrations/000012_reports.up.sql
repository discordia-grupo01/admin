-- Réplica de los reportes de servers para el expediente (HU #81). Cada fila es
-- la última foto conocida del reporte (servers.report_updated); snapshot_at es
-- el occurred_at del evento aplicado, y solo una foto más nueva la reemplaza.
--
-- Sin claves foráneas a servers ni a users: el evento puede llegar antes que la
-- entidad. No guarda quién reportó ni quién resolvió (minimización de datos) y
-- tampoco el autor de un reporte de mensaje: se resuelve al consultar, cruzando
-- message_id con la actividad.
CREATE TABLE IF NOT EXISTS reports (
    report_id        UUID PRIMARY KEY,
    server_id        UUID NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('user', 'message')),
    reported_user_id UUID,
    channel_id       UUID,
    message_id       UUID,
    reason           TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'resolved', 'dismissed')),
    created_at       TIMESTAMPTZ NOT NULL,
    resolved_at      TIMESTAMPTZ,
    snapshot_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reports_server
    ON reports (server_id, created_at DESC, report_id);
CREATE INDEX IF NOT EXISTS idx_reports_reported_user
    ON reports (reported_user_id, created_at DESC, report_id) WHERE reported_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reports_message
    ON reports (message_id) WHERE message_id IS NOT NULL;
