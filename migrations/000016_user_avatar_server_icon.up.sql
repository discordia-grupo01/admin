-- Imágenes del backoffice. Se guarda solo la dirección de la imagen, tal como
-- llega en el evento (URL pública de Supabase Storage o ruta del gateway); el
-- archivo sigue en el servicio dueño.
--
-- users.avatar_url llega en users.profile_updated. NULL = todavía no llegó un
-- perfil con avatar; '' = el usuario no tiene foto. Mientras sea NULL, el perfil
-- se acepta aunque su versión sea igual a la guardada: así el backfill de
-- identify completa el avatar de los usuarios que admin ya tenía.
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- servers.icon_url llega en servers.server_updated. '' = sin ícono. Las fotos
-- que admin ya tenía no lo traían: las completa el backfill de servers.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS icon_url TEXT;
