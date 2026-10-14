-- Staff de la plataforma: quién puede usar los endpoints /v1/admin. Se siembra
-- desde STAFF_USER_IDS al arrancar el servicio; no hay endpoint para
-- auto-asignarse el rol.
CREATE TABLE IF NOT EXISTS staff_members (
    user_id    UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
