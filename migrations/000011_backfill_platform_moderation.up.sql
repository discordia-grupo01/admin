-- Reconstruye el historial de acciones del staff anteriores a 000012 desde el
-- outbox, que conserva el payload de cada evento publicado. El evento no trae
-- al actor, así que actor_id queda nulo (la API no lo expone igual).
-- El id es el event_id: correr la migración dos veces no duplica filas.
INSERT INTO moderation_actions (id, scope, kind, target_user_id, server_id, occurred_at)
SELECT (payload->>'event_id')::uuid,
       'platform',
       CASE routing_key
            WHEN 'admin.user_suspended'            THEN 'suspension'
            WHEN 'admin.user_reactivated'          THEN 'reactivation'
            WHEN 'admin.server_suspended'          THEN 'suspension'
            WHEN 'admin.server_reactivated'        THEN 'reactivation'
            WHEN 'admin.server_deletion_requested' THEN 'deletion_requested'
       END,
       CASE WHEN routing_key LIKE 'admin.user_%' THEN (payload->>'user_id')::uuid END,
       CASE WHEN routing_key LIKE 'admin.server_%' THEN (payload->>'server_id')::uuid END,
       (payload->>'occurred_at')::timestamptz
  FROM outbox_events
 WHERE routing_key IN ('admin.user_suspended', 'admin.user_reactivated',
                       'admin.server_suspended', 'admin.server_reactivated',
                       'admin.server_deletion_requested')
   AND payload->>'event_id' IS NOT NULL
   AND payload->>'occurred_at' IS NOT NULL
ON CONFLICT (id) DO NOTHING;
