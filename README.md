# discordia-admin

Servicio de **Administración** de Discordia: la API del backoffice para el staff de la plataforma. Cubre las historias #29 (suspender usuario), #30 (reactivar usuario), #31 (listado de usuarios con filtros) y #32 (listado de servidores), y la optativa #81 (expediente de investigación de un usuario o un servidor).

> **Estado**: implementados los cuatro endpoints de las historias (#29 a #32), el expediente (#81), la base del servicio (configuración, autenticación de staff, outbox, RabbitMQ, `/livez` y `/readyz`) y el alta de staff. Ver [`openapi.yaml`](openapi.yaml).

## Stack
- Lenguaje: Go
- Framework: Gin (github.com/gin-gonic/gin)
- BDD: PostgreSQL (base propia; local con docker-compose, producción en un schema `admin` de Supabase)
- Mensajería: RabbitMQ, exchange `discordia.events` (tipos de evento en [`events-contract`](https://github.com/discordia-grupo01/events-contract))

## Cómo funciona
- **Réplica por eventos**: admin no consulta a identify ni a servers. Arma su propia copia de usuarios y servidores a partir de los eventos que ellos publican.
- **Outbox transaccional**: cada cambio de negocio inserta su evento en `outbox_events` en la misma transacción; un poller lo publica en RabbitMQ. Un corte del broker demora el evento, no lo pierde.
- **Staff**: los endpoints `/v1/admin/*` exigen un JWT válido (el mismo `JWT_SECRET` que identify, HS256) de un usuario que esté en la tabla `staff_members`. Al sembrar staff, admin publica eventos firmados `admin.staff_member_added` para que otros servicios mantengan su propia proyección de autorización.

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
RabbitMQ no guarda los eventos publicados antes de que exista la cola de un consumidor. Si identify, servers, messaging o voz se despliegan (o se reinician con la cola vacía) después de que ya había usuarios o servidores suspendidos, no se enteran de ellos. Para ponerlos al día:

```bash
make republish-suspensions ARGS=--dry-run   # cuenta las suspensiones vigentes (usuarios y servidores), sin publicar
make republish-suspensions                  # publica un admin.user_suspended por cada usuario suspendido y un admin.server_suspended por cada servidor suspendido
```

Primero salen los usuarios y después los servidores. Un servidor suspendido que ya fue borrado no se vuelve a publicar. Si falla la publicación de los servidores, el error aclara cuántas suspensiones de usuarios ya habían salido; repetir el comando es seguro.

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
| ADMIN_EVENTS_HMAC_SECRET | - | **Obligatoria.** Secreto con el que se firman los eventos `admin.*`, incluidos los cambios de staff. |
| STAFF_USER_IDS | - | UUIDs de usuarios a sembrar como staff, separados por coma. |
| OUTBOX_POLL_INTERVAL | 2s | Cada cuánto se publican los eventos pendientes. |
| ACTIVITY_RETENTION_DAYS | 90 | Días que se conserva la actividad de los usuarios para el expediente. Entero mayor a 0; si es inválido el servicio no arranca. |

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
│   ├── handler/             router y handlers HTTP (health, usuarios, servidores, suspensión, expediente)
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
- `make mutation` - mutation testing de `internal/service` (unos segundos; ver [`docs/mutation-testing`](docs/mutation-testing/README.md)).
- `make build` - compila el binario en `bin/api`.
- `make migrate` - aplica las migraciones pendientes (lee la conexión de `.env`).
- `make seed-staff EMAIL=<email>` - da de alta como staff al usuario con ese email.
- `make republish-suspensions [ARGS=--dry-run]` - vuelve a publicar las suspensiones vigentes (de usuarios y de servidores) para un consumidor nuevo.

## Tests
Cobertura mínima: 70 % (requisito del proyecto). El CI la verifica en cada PR, con Postgres y RabbitMQ de servicio.

## Endpoints
Ver [`openapi.yaml`](openapi.yaml).

## Eventos
Esquemas en [`discordia-docs`](https://github.com/discordia-grupo01/docs) (`asyncapi/`) y tipos en [`events-contract`](https://github.com/discordia-grupo01/events-contract).

**Consume** (cola `admin.users`, ack manual): `users.account_registered`, `users.profile_updated` y `users.email_verified`. Alimentan la réplica de usuarios del listado. Son idempotentes y toleran desorden: el perfil se aplica solo si su `version` es mayor a la guardada, un usuario no aparece hasta que llega su alta, y la verificación del email nunca vuelve atrás (puede llegar antes que el alta). El listado muestra `pending` a quien no confirmó su email y no está suspendido; `status` en la base sigue siendo solo el estado de suspensión. Los mensajes inválidos (JSON roto, id que no es UUID, etc.) se descartan y quedan en `consumed_events`; las fallas de base se reencolan.

**Publica** (outbox → `discordia.events`): `admin.user_suspended`, con `user_id` y `version`, sin motivo ni email. Se escribe en `outbox_events` en la misma transacción que el cambio de estado, así que una caída de RabbitMQ lo demora pero no lo pierde. Lleva una firma HMAC-SHA256 (`ADMIN_EVENTS_HMAC_SECRET`, helper `Sign`/`Verify` de `events-contract`) que cada consumidor tiene que verificar antes de aplicarlo; `version` sube con cada suspensión o reactivación del usuario y los consumidores ignoran las que no son mayores a la que ya tienen. `DELETE /v1/admin/users/{id}/suspension` publica `admin.user_reactivated` con el mismo esquema (y la `version` siguiente); la firma incluye la routing key, así que un evento de suspensión no puede reenviarse como reactivación.

**Cambios en vivo para el backoffice.** Cuando cambia algo que el backoffice muestra, admin publica `admin.backoffice_changed` con `resource` (`users`, `servers`, `reports`, `staff`, `dossier` o `metrics`) y la lista de miembros del staff a los que hay que avisar (`audience_user_ids`, leída de `staff_members`). No lleva datos ni firma: `notifications` lo empuja por WebSocket a cada miembro del staff y la pantalla vuelve a pedir lo que muestra, así nadie tiene que recargar. Se publica después de aplicar cada evento de `identify` y `servers` (alta de cuenta, perfil, email, servidor, miembros, reportes, acciones de moderación) y de cada acción de staff que termina bien (suspender o reactivar un usuario o un servidor, borrar un servidor). El aviso de `metrics` sale como mucho una vez cada 3 s. Si no se puede avisar, solo se registra un warning: nunca cambia el resultado de la acción ni del evento.

**Consistencia eventual**: `POST /v1/admin/users/{id}/suspension` suspende al usuario en admin al instante, pero identify, servers, messaging y voz lo bloquean recién cuando procesan el evento (normalmente menos de un segundo); con `DELETE`, igual al revés. Por esa ventana el usuario podría operar unos instantes; no se hace ninguna llamada síncrona para cerrarla.

**Consume** (cola `admin.servers`, ack manual): `servers.server_updated`, `servers.server_deleted`, `servers.member_joined` y `servers.member_left`, más `servers.report_updated` y `servers.moderation_action_recorded` para el expediente (ver abajo). Los primeros alimentan la réplica de servidores del listado. Ninguno trae versión, así que se ordenan por `occurred_at`: gana la foto de servidor más nueva y, por cada par servidor-usuario, el último evento de membresía. `server_deleted` deja el servidor marcado como borrado (un `server_updated` tardío no lo resucita) y descarta sus membresías. Un servidor no aparece hasta que llega su `server_updated`. Los mensajes inválidos se descartan y quedan en `consumed_events`; las fallas de base se reencolan.

**Eliminación de servidores** (HU #68). `DELETE /v1/admin/servers/{id}` (con `confirm_name` en el cuerpo) publica `admin.server_deletion_requested`, con `server_id` y `version`, firmado igual que los de usuario. Es un **pedido**, no el hecho: admin solo registra el pedido (`deletion_requested_at` y `deletion_requested_by` en `servers`) y sube `moderation_version`; el borrado lo hace servers, que después publica `servers.server_deleted`, y recién entonces el servidor sale del listado. Mientras tanto (normalmente unos segundos) sigue apareciendo. Si el evento se pierde, el servidor no se borra y el pedido se puede repetir: cada uno lleva una `version` mayor y servers es idempotente.

**Suspensión de servidores** (HU #69). `POST /v1/admin/servers/{id}/suspension` suspende el servidor y publica `admin.server_suspended`, con `server_id` y `version`, firmado igual que los demás; `DELETE` sobre la misma ruta publica `admin.server_reactivated` con la `version` siguiente. El servidor conserva todo su contenido, pero servers, messaging y voz lo bloquean al procesar el evento. El estado vive en `servers.suspended_at` (no hay columna `status`: se deriva de ahí) y `suspended_by` guarda quién lo hizo. Un servidor suspendido sigue en el listado con `suspended_at` informado y se filtra con `GET /v1/admin/servers?status=suspended|active`. La `version` es `moderation_version`, el mismo contador que usan los pedidos de eliminación: ordena suspender, reactivar y eliminar. Un `servers.server_updated` posterior no levanta la suspensión (solo toca nombre, dueño y fechas). Se puede pedir la eliminación de un servidor suspendido.

**Consistencia eventual de los servidores**: la suspensión y la reactivación se ven en admin al instante, pero servers, messaging y voz las aplican al procesar el evento (normalmente menos de un segundo; la suspensión de usuarios medida en prod tardó entre 3 y 7 s). Por esa ventana alguien podría seguir usando el servidor unos instantes, y una eliminación tarda unos segundos en desaparecer del listado.

**Orden de despliegue**: servers, messaging y voz tienen que consumir los eventos `admin.server_*` **antes** de que se despliegue este servicio. Si admin va primero, RabbitMQ descarta los eventos que no tienen una cola enlazada: un pedido de eliminación se pierde sin avisar (el servidor sigue visible y se puede repetir) y una suspensión no llega a nadie, aunque en admin figure suspendida. Para ese segundo caso, y para cualquier consumidor que se despliegue después, está `make republish-suspensions`.

## Expediente de investigación (HU #81)

Cuatro `GET` bajo `/v1/admin` (solo staff; sin token `401`, sin ser staff `403`):

| Ruta | Qué devuelve |
|---|---|
| `/users/{id}/dossier` | Estado y registro del usuario, última actividad y, de `servers`, `activity`, `reports` y `moderation`, el total y los 5 más recientes |
| `/users/{id}/dossier/{section}` | Una de esas secciones, paginada (`limit`, `offset`) |
| `/servers/{id}/dossier` | Datos del servidor, mensajes de los últimos 7 días UTC (con ceros) y, de `reports` y `moderation`, el total y los 5 más recientes |
| `/servers/{id}/dossier/{section}` | `reports` o `moderation`, paginada |

Admin responde **solo con datos locales**: no llama a servers ni a messaging. Para eso mantiene tres réplicas más, alimentadas por eventos (ADR-024):

| Tabla | Se llena con | Notas |
|---|---|---|
| `user_activity` | `messaging.message_created` (cola `admin.activity`), `servers.member_joined` y `member_left` | Guarda dónde y cuándo, **nunca el texto del mensaje**. Una tarea cada hora borra lo más viejo que `ACTIVITY_RETENTION_DAYS`. |
| `reports` | `servers.report_updated` | Foto completa del reporte; gana el `occurred_at` más nuevo. No guarda quién reportó ni quién resolvió. |
| `moderation_actions` | `servers.moderation_action_recorded` (acciones de moderadores, `scope = server`) y las propias del staff (`scope = platform`) | Las del staff se escriben en la misma transacción que la suspensión, la reactivación o el pedido de eliminación. No guarda quién la aplicó. |

- **Autor de un reporte de mensaje**: se resuelve al consultar, cruzando `message_id` con `user_activity`. Si el mensaje es anterior al despliegue, pasó la retención, o su evento todavía no llegó, `reported_user_id` es `null` y el reporte aparece solo en el expediente del servidor. Si el evento llega después, se atribuye solo.
- **Servidor eliminado**: servers lo borra de forma permanente, así que su expediente responde `404` y `server_deleted` purga en admin los reportes, la actividad y las acciones de moderadores de ese servidor. Se conservan las acciones del staff (`scope = platform`). Un evento que llegue después del borrado se descarta.
- **Privacidad de los mensajes**: la cola `admin.activity` usa un recorder sin cuerpo (`audit.WithoutBody`): descarta `content_preview` y las menciones, no registra los eventos procesados en `consumed_events` y, de los descartados, no guarda el cuerpo. Un test recorre todas las tablas buscando el texto.
- **Datos históricos** (migraciones): `000011` reconstruye las acciones del staff desde `outbox_events`, y `000014` las altas y bajas de servidores desde `consumed_events`. Los mensajes anteriores **no** se recuperan (volver a publicarlos re-dispararía los push de notifications): la actividad de mensajes arranca con el despliegue y la mensajería de un servidor se completa en 7 días. Servers publica además el backfill de reportes y de acciones de moderadores (`0034` y `0035`).
- **Orden de despliegue**: admin tiene que estar **antes** que servers. Si servers corre sus migraciones de backfill sin la cola de admin enlazada, RabbitMQ descarta esos eventos; en ese caso se vuelve a ejecutar el `INSERT` de la migración de servers (admin los deduplica).
- Los días de la mensajería son **UTC**.

Hasta que identify publique `users.account_registered` y servers publique `servers.server_updated`, las tablas de usuarios y servidores solo se llenan en los tests.

## Deploy

Cada push a `main` ejecuta `.github/workflows/ci.yml`, el mismo archivo del CI (también se puede lanzar a mano con *Run workflow*). Tiene tres jobs encadenados: si fallan los tests o `migrate`, no se despliega.

1. **`test`**: formato, `go build`, `go vet`, migraciones de la base de test y tests con una cobertura mínima del 70 %.
2. **`migrate`**: aplica las migraciones pendientes sobre la base de Supabase con `make migrate` (golang-migrate corre solo las que faltan según `schema_migrations`). Falla con un mensaje claro si falta algún secret.
3. **`deploy`**: entra por SSH a la VM de Oracle, actualiza el repo en `~/discordia/admin` y reconstruye el contenedor con `docker-compose.prod.yml`. Hace un smoke test de `/livez`.

`docker-compose.prod.yml` levanta solo la API: la base es Supabase y RabbitMQ es el compartido del equipo (por Tailscale). Las variables salen del `.env` que vive en la VM, en `~/discordia/admin/.env`, y no se versiona.

### Secrets del repositorio (GitHub → Settings → Secrets)

| Secret | Valor |
|---|---|
| `ADMIN_VM_HOST` | IP o nombre de la VM de Oracle donde corre admin. |
| `ADMIN_VM_USER` | Usuario SSH de esa VM. |
| `ADMIN_VM_SSH_KEY` | Clave privada con la que el pipeline entra por SSH. |
| `ADMIN_VM_HOST_KEY` | Línea de `known_hosts` de la VM (`ssh-keyscan -t ed25519 <host>`), para verificar que se conecta a la VM correcta. |
| `PROD_DATABASE_HOST` | Host del Session pooler de Supabase. |
| `DATABASE_PORT` | `5432`. |
| `DATABASE_NAME` | `postgres`. |
| `DATABASE_USER` | `postgres.<project-ref>`. |
| `DATABASE_PASSWORD` | Password de la base. |
| `DATABASE_SSLMODE` | `require`. |
| `DATABASE_SCHEMA` | Opcional. Sin cargar, las tablas van al schema `public`, que alcanza porque la base es solo de admin. Cargar `admin` solo si se comparte la base con otro servicio (identify también tiene tablas `users`, `consumed_events` y `outbox_events`). |

Los carga el equipo; no se versionan.

### Variables de la VM (`~/discordia/admin/.env`)

Las de la sección "Variables de entorno": `ENVIRONMENT=production`, `DATABASE_*` con `DATABASE_SSLMODE=require` (sin `DATABASE_SCHEMA`), `JWT_SECRET` (el mismo que identify), `RABBITMQ_URL`, `ADMIN_EVENTS_HMAC_SECRET` (el mismo que usan los demás servicios) y `STAFF_USER_IDS`. La VM tiene que estar en la red de Tailscale para que Kong llegue a `http://<ip-tailscale>:8080`: esa dirección es `ADMIN_UPSTREAM_URL` en `discordia-gateway`.

### Primer despliegue

Desplegar admin primero, con sus consumidores ya enlazados al exchange, y recién después volver a lanzar los backfills de identify y servers: RabbitMQ no guarda lo publicado antes de que exista la cola, y sin ese orden los listados de usuarios y servidores salen vacíos. Si algún consumidor (identify, servers, messaging, voz) se despliega después de que ya hubiera suspendidos, correr `make republish-suspensions` (ver "Poner al día a un consumidor nuevo").

## Health checks
- `GET /livez` - el proceso está vivo. No consulta dependencias.
- `GET /readyz` - Postgres y RabbitMQ responden. Devuelve 503 con los nombres de las que fallan.

## Logs

Cada línea que escribe el servicio es un objeto JSON por la salida estándar, con `time`, `level` (`debug`, `info`, `warning` o `error`), `service` (`admin`) y `message`. Es el mismo formato que usan `messaging`, `notifications` y `voice-sfu`.

- **Nivel configurable** con `LOG_LEVEL` (`debug`, `info`, `warning` o `error`; por defecto `info`). Un valor inválido hace fallar el arranque con un mensaje claro. Al arrancar escribe `service_started` con el nivel elegido.
- **Qué significa cada nivel:**
  - `error`: falla que impide ejecutar la funcionalidad (no se pudo guardar, la base no responde, un evento agotó los reintentos).
  - `warning`: condición anómala que no impide la funcionalidad básica (un evento descartado por inválido o por firma mala, una conexión a RabbitMQ que se reintenta, una configuración opcional que falta).
  - `info`: acción iniciada por una persona o por el sistema y trazable (evento procesado, barrido de vencimientos, pedido HTTP).
  - `debug`: contexto técnico para diagnosticar (eventos ignorados por viejos o repetidos, `/livez` y `/readyz` cuando responden bien).
- **Pedidos HTTP:** se registran como `http_request` con `method`, `route`, `status` y `duration_ms`. `route` es el **patrón** (`/v1/servers/:serverId`), nunca la ruta real ni la query string, porque ahí viajan ids, invitaciones y tokens. Un pedido sin ruta sale como `unmatched`. Un 5xx se registra como `error`.
- **Qué no se escribe:** el valor de campos llamados `password`, `token`, `ticket`, `jwt`, `authorization`, `cookie`, `secret`, `api_key`, `email` o `content` sale como `[FILTERED]`. Como red de seguridad, cualquier email o JWT que aparezca dentro de un texto (por ejemplo, en el mensaje de un error de la base) se reemplaza por `[EMAIL]` o `[JWT]`.
- **Pánicos:** se registran como `panic_recovered` con la ruta, el error y la traza, sin volcar los headers del pedido.
- El código vive en `internal/logging`. Las llamadas viejas al paquete `log` de Go también salen como JSON, porque `Setup` redirige el logger estándar.

## Trazas y métricas (OpenTelemetry)

El servicio manda trazas y métricas a cualquier backend que hable OTLP por HTTP (New Relic, Grafana Cloud, un colector propio). Está **apagado por defecto**: sin `OTEL_EXPORTER_OTLP_ENDPOINT` no se exporta nada y el servicio funciona igual.

| Variable | Para qué |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | URL base del backend. En New Relic (cuenta de EE. UU.): `https://otlp.nr-data.net` |
| `OTEL_EXPORTER_OTLP_HEADERS` | Credenciales, por ejemplo `api-key=<clave de licencia de ingesta>`. Es un secreto: va solo en las variables de entorno del despliegue, nunca en el repositorio |
| `OTEL_SERVICE_NAME` | Nombre con el que aparece el servicio (por defecto `admin`) |

- **Pedidos HTTP:** un span por pedido, llamado como el patrón de la ruta (`GET /v1/...`). Solo guarda `http.request.method`, `http.route` y `http.response.status_code`: nunca el path real, la query string ni los headers. Si el pedido trae un `traceparent` (Kong o un cliente), la traza continúa desde ahí.
- **Base de datos:** un span por consulta de `database/sql` (envoltorio de `otelsql`), con la operación y el SQL con sus `$1`, `$2`, **sin los valores**. Las consultas que no pertenecen a un pedido o a un evento (barridos y reintentos en segundo plano) no generan spans.
- **RabbitMQ:** `outbox_events.traceparent` (migración `000009`) guarda la traza del pedido que originó el evento. Cuando el *poller* lo publica crea un span `outbox publish <routing key>` dentro de esa traza y el mensaje sale con la cabecera `traceparent`. Los eventos viejos y los que no vienen de un pedido tienen `traceparent` nulo y se publican igual.
- **Consumidores:** cada evento que se consume abre un span `<routing key> process` dentro de la traza que llega en la cabecera `traceparent` del mensaje; si falla y se reintenta, el span queda marcado como error. Si el mensaje no la trae, empieza una traza nueva.
- **Qué nunca se exporta:** el valor de los parámetros SQL, el texto de un error (un filtro del exportador le borra `exception.message` y `exception.stacktrace` a todo span) ni datos del usuario.
- **Métricas HTTP:** el histograma `http.server.request.duration` (en segundos, temporalidad delta, exportado cada 60 s) se registra en el mismo middleware que arma el span, con `http.request.method`, `http.route` y `http.response.status_code`; si el estado es 5xx suma `error.type`. De ahí salen el throughput, la latencia y la tasa de errores del resumen de APM en New Relic. Los pedidos sin ruta se agrupan en `unmatched`. Nunca guarda la ruta real ni la query string.
- **Logs:** cada línea escrita dentro de un pedido lleva `trace_id` y `span_id`.

## Envío de logs (New Relic)

Además de la salida estándar, el servicio puede enviar sus logs al Log API de New Relic. Está **apagado por defecto**: solo se activa si hay `LOG_SHIP_URL` y una clave (`LOG_SHIP_API_KEY` o la `api-key` de `OTEL_EXPORTER_OTLP_HEADERS`).

| Variable | Para qué |
|---|---|
| `LOG_SHIP_URL` | URL del Log API. En New Relic (cuenta de EE. UU.): `https://log-api.newrelic.com/log/v1` |
| `LOG_SHIP_API_KEY` | Opcional: clave de ingesta para `LOG_SHIP_URL`. Si no está, se toma la `api-key` de `OTEL_EXPORTER_OTLP_HEADERS`. Es un secreto: va solo en las variables de entorno del despliegue |

- **Qué se envía:** las mismas líneas que salen por la salida estándar, con el mismo filtrado de datos sensibles (campos `password`, `token`, `ticket`, `email`, `content`, etc. y emails o JWT dentro de textos). `trace_id` y `span_id` viajan como `trace.id` y `span.id`, que es como New Relic enlaza cada log con su traza. El servicio se identifica con `service.name` (el valor de `OTEL_SERVICE_NAME`, por defecto `admin`).
- **Cómo se envía:** `internal/logging` junta las líneas en lotes de hasta 200 o cada 5 segundos, las comprime en gzip y las manda por HTTPS. Los pedidos no esperan al envío.
- **Si el backend falla:** reintenta con espera creciente (de 1 s hasta 60 s) ante errores de red, 408, 429 y 5xx; un lote rechazado de forma definitiva (por ejemplo 400 o 403) se descarta. La cola está acotada a 5.000 entradas: si se llena descarta las más viejas, para no crecer sin límite en memoria.
- **Sin bucles:** los avisos del propio envío (`log_shipping_failed`, `log_shipping_recovered`, `log_batch_discarded`, `log_entries_dropped`) salen solo por la salida estándar y nunca se reenvían.
- **Al apagar:** `logging.Flush()` intenta enviar lo que quedó pendiente durante unos 3 segundos.

## Formateo automático
Este repo se autoformatea con gofmt en cada commit. Activalo una vez por clone:
```bash
git config core.hooksPath .githooks
```
Si usan VS Code con la extensión oficial de Go, también formatea al guardar (ya configurado en .vscode/settings.json).

El CI corre `gofmt -l` como red de seguridad (falla si encuentra archivos sin formatear), por si alguien clonó sin activar el hook.
