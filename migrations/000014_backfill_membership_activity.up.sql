-- Reconstruye las altas y bajas de servidores anteriores a 000013 desde
-- consumed_events, que guarda el cuerpo de cada evento procesado. No incluye
-- mensajes: no hay de dónde recuperarlos (y volver a publicar message_created
-- dispararía de nuevo los push de notifications).
-- Se saltea lo de servidores borrados, igual que el consumidor, y lo que no
-- tenga un event_id UUID. El id es el event_id: correrla dos veces no duplica.
INSERT INTO user_activity (id, user_id, kind, server_id, occurred_at)
SELECT (b.body->>'event_id')::uuid,
       (b.body->>'user_id')::uuid,
       CASE c.routing_key WHEN 'servers.member_joined' THEN 'joined_server' ELSE 'left_server' END,
       (b.body->>'server_id')::uuid,
       (b.body->>'occurred_at')::timestamptz
  FROM consumed_events c
 CROSS JOIN LATERAL (SELECT c.raw_body::jsonb AS body) b
 WHERE c.status = 'processed'
   AND c.routing_key IN ('servers.member_joined', 'servers.member_left')
   AND b.body->>'event_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   AND NOT EXISTS (SELECT 1 FROM servers s
                    WHERE s.server_id = (b.body->>'server_id')::uuid AND s.deleted_at IS NOT NULL)
ON CONFLICT (id) DO NOTHING;
