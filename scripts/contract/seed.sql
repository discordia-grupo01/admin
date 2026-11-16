-- Datos de prueba para los tests de contrato. Solo se cargan en la base efimera
-- de contrato (ver scripts/contract-test.sh). Los ids son fijos y los usa
-- scripts/contract/seeded.toml. Cada operacion que cambia datos tiene su propia
-- fila para no depender del orden de las pruebas. No toca staff_members.
BEGIN;

TRUNCATE users, servers, server_members, reports, moderation_actions, user_activity, account_recovery_requests;

-- Usuarios: b1 con expediente completo, b2 se suspende, b3 ya esta suspendido.
INSERT INTO users (user_id, name, email, registered_at, status, suspended_at, email_verified) VALUES
  ('00000000-0000-4000-8000-0000000000b1', 'Usuario Expediente', 'expediente@example.test', now() - interval '30 days', 'active', NULL, true),
  ('00000000-0000-4000-8000-0000000000b2', 'Usuario A Suspender', 'suspender@example.test', now() - interval '20 days', 'active', NULL, true),
  ('00000000-0000-4000-8000-0000000000b3', 'Usuario Suspendido', 'suspendido@example.test', now() - interval '10 days', 'suspended', now() - interval '1 day', true);

-- Servidores: c1 con expediente, c2 se suspende, c3 ya suspendido, c4 se elimina.
INSERT INTO servers (server_id, name, owner_id, created_at, snapshot_at, suspended_at) VALUES
  ('00000000-0000-4000-8000-0000000000c1', 'Servidor Expediente', '00000000-0000-4000-8000-0000000000b1', now() - interval '25 days', now(), NULL),
  ('00000000-0000-4000-8000-0000000000c2', 'Servidor A Suspender', '00000000-0000-4000-8000-0000000000b1', now() - interval '24 days', now(), NULL),
  ('00000000-0000-4000-8000-0000000000c3', 'Servidor Suspendido', '00000000-0000-4000-8000-0000000000b1', now() - interval '23 days', now(), now() - interval '1 day'),
  ('00000000-0000-4000-8000-0000000000c4', 'Servidor A Eliminar', '00000000-0000-4000-8000-0000000000b1', now() - interval '22 days', now(), NULL);

INSERT INTO server_members (server_id, user_id, is_member, updated_at) VALUES
  ('00000000-0000-4000-8000-0000000000c1', '00000000-0000-4000-8000-0000000000b1', true, now()),
  ('00000000-0000-4000-8000-0000000000c1', '00000000-0000-4000-8000-0000000000b2', true, now());

INSERT INTO user_activity (id, user_id, kind, server_id, channel_id, channel_name, message_id, occurred_at) VALUES
  ('00000000-0000-4000-8000-0000000000e1', '00000000-0000-4000-8000-0000000000b1', 'joined_server', '00000000-0000-4000-8000-0000000000c1', NULL, NULL, NULL, now() - interval '5 days'),
  ('00000000-0000-4000-8000-0000000000e2', '00000000-0000-4000-8000-0000000000b1', 'message_sent', '00000000-0000-4000-8000-0000000000c1', '00000000-0000-4000-8000-0000000000f1', 'general', '00000000-0000-4000-8000-0000000000f2', now() - interval '4 days');

INSERT INTO reports (report_id, server_id, kind, reported_user_id, channel_id, message_id, reason, status, created_at, snapshot_at) VALUES
  ('00000000-0000-4000-8000-0000000000a1', '00000000-0000-4000-8000-0000000000c1', 'user', '00000000-0000-4000-8000-0000000000b1', NULL, NULL, 'spam', 'pending', now() - interval '2 days', now()),
  ('00000000-0000-4000-8000-0000000000a2', '00000000-0000-4000-8000-0000000000c1', 'message', '00000000-0000-4000-8000-0000000000b1', '00000000-0000-4000-8000-0000000000f1', '00000000-0000-4000-8000-0000000000f2', 'abuso', 'resolved', now() - interval '3 days', now());

INSERT INTO moderation_actions (id, scope, kind, target_user_id, server_id, actor_id, occurred_at) VALUES
  ('00000000-0000-4000-8000-0000000000a3', 'server', 'ban', '00000000-0000-4000-8000-0000000000b1', '00000000-0000-4000-8000-0000000000c1', NULL, now() - interval '1 day'),
  ('00000000-0000-4000-8000-0000000000a4', 'platform', 'suspension', '00000000-0000-4000-8000-0000000000b1', NULL, NULL, now() - interval '12 hours');

-- Solicitudes de recuperacion: d1 para consultar, d2 se aprueba, d3 se rechaza.
INSERT INTO account_recovery_requests (request_id, user_id, message, status, requested_at) VALUES
  ('00000000-0000-4000-8000-0000000000d1', '00000000-0000-4000-8000-0000000000b1', 'Perdi el celular del 2FA', 'pending', now() - interval '3 hours'),
  ('00000000-0000-4000-8000-0000000000d2', '00000000-0000-4000-8000-0000000000b2', 'Perdi el celular del 2FA', 'pending', now() - interval '2 hours'),
  ('00000000-0000-4000-8000-0000000000d3', '00000000-0000-4000-8000-0000000000b2', 'Perdi el celular del 2FA', 'pending', now() - interval '1 hour');

COMMIT;
