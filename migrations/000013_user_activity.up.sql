-- Actividad de los usuarios para el expediente (HU #81): mensajes enviados en
-- canales de servidores y altas o bajas de servidores. Cada fila es un hecho
-- inmutable: su id es el event_id del evento de origen, así que procesar dos
-- veces el mismo evento no duplica nada.
--
-- Guarda dónde y cuándo, nunca qué: el texto de un mensaje no se persiste
-- (minimización de datos). channel_name es opcional en el evento.
-- Sin claves foráneas: los eventos pueden llegar antes que la entidad.
-- Una tarea periódica borra lo más viejo que ACTIVITY_RETENTION_DAYS.
CREATE TABLE IF NOT EXISTS user_activity (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('message_sent', 'joined_server', 'left_server')),
    server_id    UUID NOT NULL,
    channel_id   UUID,
    channel_name TEXT,
    message_id   UUID,
    occurred_at  TIMESTAMPTZ NOT NULL
);

-- La página del expediente de un usuario, la mensajería por día de un servidor
-- y la atribución del autor de un mensaje reportado.
CREATE INDEX IF NOT EXISTS idx_user_activity_user
    ON user_activity (user_id, occurred_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_user_activity_server
    ON user_activity (server_id, kind, occurred_at);
CREATE INDEX IF NOT EXISTS idx_user_activity_message
    ON user_activity (message_id) WHERE message_id IS NOT NULL;
