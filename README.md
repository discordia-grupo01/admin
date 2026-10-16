# discordia-admin

Servicio de **Administración** de Discordia: la API del backoffice para el staff de la plataforma. Cubre las historias #29 (suspender usuario), #30 (reactivar usuario), #31 (listado de usuarios con filtros) y #32 (listado de servidores).

> **Estado**: implementados los cuatro endpoints de las historias (#29 a #32), la base del servicio (configuración, autenticación de staff, outbox, RabbitMQ, `/livez` y `/readyz`) y el alta de staff. Ver [`openapi.yaml`](openapi.yaml).

## Stack
- Lenguaje: Go
- Framework: Gin (github.com/gin-gonic/gin)
- BDD: PostgreSQL (base propia; local con docker-compose, producción en un schema `admin` de Supabase)
- Mensajería: RabbitMQ, exchange `discordia.events` (tipos de evento en [`events-contract`](https://github.com/discordia-grupo01/events-contract))

## Cómo funciona
- **Réplica por eventos**: admin no consulta a identify ni a servers. Arma su propia copia de usuarios y servidores a partir de los eventos que ellos publican.
- **Outbox transaccional**: cada cambio de negocio inserta su evento en `outbox_events` en la misma transacción; un poller lo publica en RabbitMQ. Un corte del broker demora el evento, no lo pierde.
- **Staff**: los endpoints `/v1/admin/*` exigen un JWT válido (el mismo `JWT_SECRET` que identify, HS256) de un usuario que esté en la tabla `staff_members`. El staff se siembra al arrancar desde `STAFF_USER_IDS`; no hay forma de auto-asignarse el rol desde la API.

## Setup local
Requiere Go 1.22+ y Docker.

```bash
cp .env.example .env                    # completar JWT_SECRET y ADMIN_EVENTS_HMAC_SECRET (openssl rand -hex 32)
docker compose up -d db rabbitmq
make migrate
make run
```

Postgres queda en `localhost:5433` (no 5432, para no chocar con otros servicios) y la consola de RabbitMQ en http://localhost:15672 (guest / guest).

## Dar de alta al staff
El rol de staff no se puede auto-asignar desde la API: se otorga por fuera, de dos maneras.

- **Por UUID**: poner el UUID del usuario (de identify) en `STAFF_USER_IDS`. Se siembra en cada arranque (es idempotente).
- **Por email**: si el usuario ya está en la réplica de admin (se registró en identify y su alta llegó por `users.account_registered`):
  ```bash
  make seed-staff EMAIL=manu@example.com
  ```
  Usa la misma conexión `DATABASE_*` que el servicio y no necesita `JWT_SECRET` ni RabbitMQ. Es idempotente.

Para quitarle el rol a alguien: borrar su fila de `staff_members` y sacarlo de `STAFF_USER_IDS` (si no, se vuelve a sembrar al arrancar).

## Poner al día a un consumidor nuevo
RabbitMQ no guarda los eventos publicados antes de que exista la cola de un consumidor. Si identify, servers, messaging o voz se despliegan (o se reinician con la cola vacía) después de que ya había usuarios suspendidos, no se enteran de ellos. Para ponerlos al día:

```bash
make republish-suspensions ARGS=--dry-run   # cuenta las suspensiones vigentes, sin publicar
make republish-suspensions                  # publica un admin.user_suspended por cada suspendido
```

Cada evento sale con la `version` vigente del usuario, un `event_id` nuevo y su propia firma. Es seguro repetirlo: los consumidores ignoran lo que ya aplicaron. Publica directo al broker (no pasa por el outbox), usa `DATABASE_*`, `RABBITMQ_URL` y `ADMIN_EVENTS_HMAC_SECRET`, y si RabbitMQ no responde falla con un error. Hay que correrlo una vez por cada consumidor nuevo.

## Variables de entorno
| Variable | Default | Descripción |
|---|---|---|
| ENVIRONMENT | development | `development` o `production`. |
| HOST | 0.0.0.0 | IP donde escucha el servicio. |
| PORT | 8080 | Puerto HTTP. |
| DATABASE_HOST | localhost | Host de PostgreSQL. |
| DATABASE_PORT | 5432 | Puerto de PostgreSQL (`.env.example` usa 5433, el del docker-compose). |
| DATABASE_NAME | discordia_admin | Nombre de la base. |
| DATABASE_USER | discordia | Usuario. |
| DATABASE_PASSWORD | - | Password. |
| DATABASE_SSLMODE | disable | `require` en Supabase. |
| DATABASE_SCHEMA | - | Fija el `search_path` (ej. `admin` en Supabase). Vacío en local. |
| JWT_SECRET | - | **Obligatoria.** Secreto con el que identify firma los JWT. |
| RABBITMQ_URL | - | **Obligatoria.** URL de conexión a RabbitMQ. |
| ADMIN_EVENTS_HMAC_SECRET | - | **Obligatoria.** Secreto con el que se firman los eventos `admin.*`. |
| STAFF_USER_IDS | - | UUIDs de usuarios a sembrar como staff, separados por coma. |
| OUTBOX_POLL_INTERVAL | 2s | Cada cuánto se publican los eventos pendientes. |

`.env.example` solo trae la estructura (sin valores sensibles). Los valores reales van en tu propio `.env`, que nunca se sube (ya está en `.gitignore`). Si falta una variable obligatoria o un valor es inválido, el servicio no arranca y lo dice.

## Estructura de carpetas
```
discordia-admin/
├── cmd/
│   ├── api/main.go          arranque: config, base, consumers, outbox, router y apagado ordenado
│   ├── seedstaff/main.go    herramienta: da de alta staff por email
│   └── republishsuspensions/main.go  herramienta: re-publica las suspensiones vigentes
├── internal/
│   ├── config/              carga y validación del entorno
│   ├── apierr/              formato de error {error:{code,message,details}}
│   ├── middleware/          autenticación (JWT) y autorización (staff)
│   ├── handler/             router y handlers HTTP (health, usuarios, servidores, suspensión)
│   ├── service/             lógica de negocio
│   ├── repository/          acceso a PostgreSQL
│   ├── audit/               registro de eventos consumidos
│   ├── outbox/              outbox transaccional y poller
│   ├── events/              publisher y consumer de RabbitMQ
│   └── testutil/            helpers de los tests de integración
├── migrations/              migraciones de la base (golang-migrate)
├── openapi.yaml             contrato de la API REST
├── docker-compose.yml       Postgres + RabbitMQ (+ el servicio) para desarrollo local
├── Dockerfile               build multi-stage para la imagen de producción
└── Makefile                 comandos run/test/build/migrate
```

## Comandos (Makefile)
- `make run` - corre el servicio localmente.
- `make test` - tests unitarios con cobertura. Los de integración se saltean solos si no hay servicios.
- `make test-integration` - además corre los tests de integración contra el Postgres y el RabbitMQ de docker-compose (usa una base aparte, `discordia_admin_test`, porque esos tests vacían tablas).
- `make build` - compila el binario en `bin/api`.
- `make migrate` - aplica las migraciones pendientes (lee la conexión de `.env`).
- `make seed-staff EMAIL=<email>` - da de alta como staff al usuario con ese email.
- `make republish-suspensions [ARGS=--dry-run]` - vuelve a publicar las suspensiones vigentes para un consumidor nuevo.

## Tests
Cobertura mínima: 70 % (requisito del proyecto). El CI la verifica en cada PR, con Postgres y RabbitMQ de servicio.

## Endpoints
Ver [`openapi.yaml`](openapi.yaml).

## Eventos
Esquemas en [`discordia-docs`](https://github.com/discordia-grupo01/docs) (`asyncapi/`) y tipos en [`events-contract`](https://github.com/discordia-grupo01/events-contract).

**Consume** (cola `admin.users`, ack manual): `users.account_registered` y `users.profile_updated`. Alimentan la réplica de usuarios del listado. Son idempotentes y toleran desorden: el perfil se aplica solo si su `version` es mayor a la guardada, y un usuario no aparece hasta que llega su alta. Los mensajes inválidos (JSON roto, id que no es UUID, etc.) se descartan y quedan en `consumed_events`; las fallas de base se reencolan.

**Publica** (outbox → `discordia.events`): `admin.user_suspended`, con `user_id` y `version`, sin motivo ni email. Se escribe en `outbox_events` en la misma transacción que el cambio de estado, así que una caída de RabbitMQ lo demora pero no lo pierde. Lleva una firma HMAC-SHA256 (`ADMIN_EVENTS_HMAC_SECRET`, helper `Sign`/`Verify` de `events-contract`) que cada consumidor tiene que verificar antes de aplicarlo; `version` sube con cada suspensión o reactivación del usuario y los consumidores ignoran las que no son mayores a la que ya tienen. `DELETE /v1/admin/users/{id}/suspension` publica `admin.user_reactivated` con el mismo esquema (y la `version` siguiente); la firma incluye la routing key, así que un evento de suspensión no puede reenviarse como reactivación.

**Consistencia eventual**: `POST /v1/admin/users/{id}/suspension` suspende al usuario en admin al instante, pero identify, servers, messaging y voz lo bloquean recién cuando procesan el evento (normalmente menos de un segundo); con `DELETE`, igual al revés. Por esa ventana el usuario podría operar unos instantes; no se hace ninguna llamada síncrona para cerrarla.

**Consume** (cola `admin.servers`, ack manual): `servers.server_updated`, `servers.server_deleted`, `servers.member_joined` y `servers.member_left`. Alimentan la réplica de servidores del listado. Ninguno trae versión, así que se ordenan por `occurred_at`: gana la foto de servidor más nueva y, por cada par servidor-usuario, el último evento de membresía. `server_deleted` deja el servidor marcado como borrado (un `server_updated` tardío no lo resucita) y descarta sus membresías. Un servidor no aparece hasta que llega su `server_updated`. Los mensajes inválidos se descartan y quedan en `consumed_events`; las fallas de base se reencolan.

Hasta que identify publique `users.account_registered` y servers publique `servers.server_updated`, las tablas de usuarios y servidores solo se llenan en los tests.

## Deploy

Cada push a `main` ejecuta `.github/workflows/deploy.yml` (también se puede lanzar a mano con *Run workflow*). Tiene dos jobs encadenados: si falla `migrate`, no se despliega.

1. **`migrate`**: aplica las migraciones pendientes sobre la base de Supabase con `make migrate` (golang-migrate corre solo las que faltan según `schema_migrations`). Falla con un mensaje claro si falta algún secret.
2. **`deploy`**: entra por SSH a la VM de Oracle, actualiza el repo en `~/discordia/admin` y reconstruye el contenedor con `docker-compose.prod.yml`. Hace un smoke test de `/livez`.

`docker-compose.prod.yml` levanta solo la API: la base es Supabase y RabbitMQ es el compartido del equipo (por Tailscale). Las variables salen del `.env` que vive en la VM, en `~/discordia/admin/.env`, y no se versiona.

### Secrets del repositorio (GitHub → Settings → Secrets)

| Secret | Valor |
|---|---|
| `ADMIN_VM_HOST` | IP o nombre de la VM de Oracle donde corre admin. |
| `ADMIN_VM_USER` | Usuario SSH de esa VM. |
| `ADMIN_VM_SSH_KEY` | Clave privada con la que el pipeline entra por SSH. |
| `ADMIN_VM_HOST_KEY` | Línea de `known_hosts` de la VM (`ssh-keyscan -t ed25519 <host>`), para verificar que se conecta a la VM correcta. |
| `PROD_DATABASE_HOST` | Host del Session pooler de Supabase. |
| `PROD_DATABASE_PORT` | `5432`. |
| `PROD_DATABASE_NAME` | `postgres`. |
| `PROD_DATABASE_USER` | `postgres.<project-ref>`. |
| `PROD_DATABASE_PASSWORD` | Password de la base. |
| `PROD_DATABASE_SSLMODE` | `require`. |
| `PROD_DATABASE_SCHEMA` | Opcional. Sin cargar, las tablas van al schema `public`, que alcanza porque la base es solo de admin. Cargar `admin` solo si se comparte la base con otro servicio (identify también tiene tablas `users`, `consumed_events` y `outbox_events`). |

Los carga el equipo; no se versionan.

### Variables de la VM (`~/discordia/admin/.env`)

Las de la sección "Variables de entorno": `ENVIRONMENT=production`, `DATABASE_*` con `DATABASE_SSLMODE=require` (sin `DATABASE_SCHEMA`), `JWT_SECRET` (el mismo que identify), `RABBITMQ_URL`, `ADMIN_EVENTS_HMAC_SECRET` (el mismo que usan los demás servicios) y `STAFF_USER_IDS`. La VM tiene que estar en la red de Tailscale para que Kong llegue a `http://<ip-tailscale>:8080`: esa dirección es `ADMIN_UPSTREAM_URL` en `discordia-gateway`.

### Primer despliegue

Desplegar admin primero, con sus consumidores ya enlazados al exchange, y recién después volver a lanzar los backfills de identify y servers: RabbitMQ no guarda lo publicado antes de que exista la cola, y sin ese orden los listados de usuarios y servidores salen vacíos. Si algún consumidor (identify, servers, messaging, voz) se despliega después de que ya hubiera suspendidos, correr `make republish-suspensions` (ver "Poner al día a un consumidor nuevo").

## Health checks
- `GET /livez` - el proceso está vivo. No consulta dependencias.
- `GET /readyz` - Postgres y RabbitMQ responden. Devuelve 503 con los nombres de las que fallan.

## Formateo automático
Este repo se autoformatea con gofmt en cada commit. Activalo una vez por clone:
```bash
git config core.hooksPath .githooks
```
Si usan VS Code con la extensión oficial de Go, también formatea al guardar (ya configurado en .vscode/settings.json).

El CI corre `gofmt -l` como red de seguridad (falla si encuentra archivos sin formatear), por si alguien clonó sin activar el hook.
