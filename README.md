# discordia-admin

Servicio de **Administración** de Discordia: la API del backoffice para el staff de la plataforma. Cubre las historias #29 (suspender usuario), #30 (reactivar usuario), #31 (listado de usuarios con filtros) y #32 (listado de servidores).

> **Estado**: la base del servicio está lista (configuración, autenticación de staff, outbox, RabbitMQ, `/livez` y `/readyz`). Los endpoints de `/v1/admin` están definidos en [`openapi.yaml`](openapi.yaml) y se implementan en las ramas de cada historia.

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

Para entrar como staff, poner en `STAFF_USER_IDS` el UUID de un usuario registrado en identify.

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
├── cmd/api/main.go          arranque: config, base, outbox, router y apagado ordenado
├── internal/
│   ├── config/              carga y validación del entorno
│   ├── apierr/              formato de error {error:{code,message,details}}
│   ├── middleware/          autenticación (JWT) y autorización (staff)
│   ├── handler/             router y handlers HTTP (health)
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

## Tests
Cobertura mínima: 70 % (requisito del proyecto). El CI la verifica en cada PR, con Postgres y RabbitMQ de servicio.

## Endpoints
Ver [`openapi.yaml`](openapi.yaml).

## Eventos
Se agregan junto con cada historia: `admin.user_suspended` y `admin.user_reactivated` (publica, firmados con HMAC), y los de `users.*` y `servers.*` que consume para armar sus listados.

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
