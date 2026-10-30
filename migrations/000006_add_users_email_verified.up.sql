-- Si el usuario ya confirmó su email en identify (users.account_registered y
-- users.email_verified). No forma parte de `status`, que es solo el estado de
-- suspensión y le pertenece a admin: el listado calcula "pending" a partir de
-- las dos columnas. Arranca en FALSE para todos; el backfill de identify
-- (users.email_verified por cada cuenta ya verificada) lo completa.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;
