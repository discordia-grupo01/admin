# admin

Servicio de administración de Discordia.

## Tabla de contenidos

- [Introducción](#introducción)
- [Pre-requisitos](#pre-requisitos)
- [Cómo correr](#cómo-correr)
- [Variables de entorno](#variables-de-entorno)
- [Comandos Docker](#comandos-docker)
- [Tests](#tests)
- [Más documentación](#más-documentación)

## Introducción

`admin` es la API del backoffice: lo que usa el staff de la plataforma para controlar abusos. Se divide en tres partes:

- **Usuarios:** listar con filtros, suspender y reactivar cuentas, y revisar los pedidos de recuperación de cuenta de quien perdió su segundo factor.
- **Servidores:** listar, suspender, reactivar y eliminar servidores completos.
- **Expediente:** reunir en un solo lugar todo lo que se sabe de un usuario o de un servidor (reportes, acciones de moderación y actividad) para investigar.

Está hecho en Go con Gin y guarda todo en su propia base PostgreSQL. No le pregunta nada a `identify-service` ni a `servers`: arma su propia copia de usuarios y servidores a partir de los eventos que ellos publican.

Cuando el staff suspende una cuenta o un servidor, `admin` publica un evento firmado (HMAC) que consumen `identify-service`, `servers`, `messaging` y `voice-sfu` para bloquear. Antes de publicar anota el evento en una tabla de su base dentro de la misma transacción, así que si RabbitMQ se cae el evento no se pierde: solo se retrasa.

Todas las rutas `/v1/admin/*` exigen un JWT válido de un usuario que figure en la tabla de staff. El rol de staff no se puede asignar desde la API: se otorga por fuera, como se explica más abajo.

La API REST está descripta en [`openapi.yaml`](openapi.yaml).

## Pre-requisitos

- **Docker** y **Docker Compose** v2.24 o más nuevo.
- **RabbitMQ**, que se levanta con el repositorio [`rabbit-mq`](https://github.com/discordia-grupo01/rabbit-mq). El servicio no arranca sin él.
- **Go 1.22** y **make**, solo si querés correr el servicio con Go en tu máquina.

## Cómo correr

### Con Docker Compose

Primero levantá RabbitMQ desde el repositorio `rabbit-mq`:

```bash
cd ../rabbit-mq
docker compose up -d
```

Después, en este repositorio, levantá el servicio con su base PostgreSQL:

```bash
docker compose up --build
```

Antes de arrancar el servicio, un contenedor `migrate` crea las tablas y termina. Cuando todo esté arriba, comprobá que el servicio esté vivo:

```bash
curl http://localhost:8080/readyz
```

El servicio se conecta a RabbitMQ en `host.docker.internal:5672`, que desde un contenedor es tu máquina. Si lo levantaste en otro lado, completá `RABBITMQ_URL_DOCKER` en el `.env`.

Para llamar a la API necesitás un JWT firmado con `JWT_SECRET` y cuyo usuario esté en el staff. El compose deja como staff al usuario `11111111-1111-4111-8111-111111111111`, así que alcanza con un token con ese `sub`. Los usuarios de verdad los emite `identify-service`.

Si querés cambiar puertos, usuario de la base o los secretos de prueba, copiá el archivo de ejemplo y editalo antes de levantar:

```bash
cp .env.example .env
```

Este compose levanta solo lo que es de este servicio. Para el sistema completo (con el gateway y los demás servicios) usá el compose del repositorio `infrastructure`.

### Para desarrollar

Si vas a tocar el código, te conviene correr el servicio con Go en tu máquina y dejar en un contenedor solo la base:

```bash
cp .env.example .env
docker compose up -d db
make migrate
make run
```

Con el `.env` de ejemplo, el servicio se conecta a la base que acabás de levantar y a RabbitMQ en `localhost`.

### Dar de alta al staff

Hay dos formas:

- **Por UUID:** poner el id del usuario (el de `identify-service`) en `STAFF_USER_IDS`. Se siembra en cada arranque y es idempotente.
- **Por email:** si el usuario ya se registró y `admin` se enteró por el evento de registro, `make seed-staff EMAIL=persona@example.com` lo da de alta sin tocar RabbitMQ ni `JWT_SECRET`.

Para sacarle el rol a alguien, borrá su fila de `staff_members` y sacalo de `STAFF_USER_IDS`.

## Variables de entorno

Se leen del entorno o de un archivo `.env`. El archivo [`.env.example`](.env.example) trae valores listos para correr en local.

| Variable | Default | Descripción |
|---|---|---|
| `ENVIRONMENT` | `development` | `development` o `production`. |
| `HOST` | `0.0.0.0` | IP donde escucha el servicio. |
| `PORT` | `8080` | Puerto donde escucha el servicio. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warning` o `error`. |
| `DATABASE_HOST` | `localhost` | Host de PostgreSQL. |
| `DATABASE_PORT` | `5432` | Puerto de PostgreSQL. El `.env.example` usa `5433`, el del compose. |
| `DATABASE_NAME` | `discordia_admin` | Nombre de la base. |
| `DATABASE_USER` | `discordia` | Usuario de la base. |
| `DATABASE_PASSWORD` | vacío | Contraseña de la base. |
| `DATABASE_SSLMODE` | `disable` | `disable` en local, `require` con Supabase. |
| `DATABASE_SCHEMA` | vacío | Schema del servicio dentro de la base. Vacío en local; en Supabase es `admin`. |
| `JWT_SECRET` | obligatoria | Secreto con el que `identify-service` firma los JWT. Es un secreto. |
| `RABBITMQ_URL` | obligatoria | Conexión a RabbitMQ. |
| `ADMIN_EVENTS_HMAC_SECRET` | obligatoria | Secreto con el que se firman los eventos `admin.*`. Es el mismo que usan los servicios que los consumen. |
| `STAFF_USER_IDS` | vacío | Ids de usuarios a dejar como staff, separados por coma. |
| `OUTBOX_POLL_INTERVAL` | `2s` | Cada cuánto se publican los eventos pendientes. |
| `ACTIVITY_RETENTION_DAYS` | `90` | Días que se conserva la actividad de los usuarios para el expediente. |
| `VOICE_DLQ_QUEUES` | vacío | Colas de eventos fallidos de `voice-sfu` que revisan `make voice-dlq-check` y `make voice-dlq-replay`. |
| `VOICE_DLQ_THRESHOLD` | `0` | Cantidad de mensajes a partir de la cual `voice-dlq-check` avisa. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | vacío | Backend OpenTelemetry (por ejemplo New Relic). Vacío = sin trazas ni métricas. |
| `OTEL_EXPORTER_OTLP_HEADERS` | vacío | Credenciales del backend. Es un secreto. |
| `LOG_SHIP_URL` | vacío | Si se completa, también manda los logs a New Relic. |

Variables que usa solo `docker-compose.yml`:

| Variable | Default | Descripción |
|---|---|---|
| `API_PORT` | `8080` | Puerto del servicio en tu máquina. |
| `DATABASE_PORT` | `5433` | Puerto de PostgreSQL en tu máquina. |
| `RABBITMQ_URL_DOCKER` | `amqp://guest:guest@host.docker.internal:5672/` | Conexión a RabbitMQ para el servicio que corre dentro del compose. |

## Comandos Docker

Todos desde la raíz del repositorio.

| Qué querés hacer | Comando |
|---|---|
| Levantar todo | `docker compose up --build` |
| Levantar todo en segundo plano | `docker compose up -d --build` |
| Levantar solo la base | `docker compose up -d db` |
| Ver los logs del servicio | `docker compose logs -f api` |
| Ver qué está corriendo | `docker compose ps` |
| Reconstruir solo el servicio | `docker compose up -d --build api` |
| Abrir una consola de la base | `docker compose exec db psql -U discordia -d discordia_admin` |
| Bajar todo, conservando los datos | `docker compose down` |
| Bajar todo y borrar los datos | `docker compose down -v` |

Las migraciones nuevas se aplican solas cada vez que levantás el compose.

Para construir y correr la imagen a mano, contra una base que ya tengas:

```bash
docker build -t admin .
docker run --rm -p 8080:8080 --env-file .env admin
```

Dentro de un contenedor, `DATABASE_HOST` y `RABBITMQ_URL` no pueden apuntar a `localhost`: tienen que usar el nombre del contenedor o `host.docker.internal`.

## Tests

Los tests unitarios no necesitan nada más:

```bash
make test
```

`make test-integration` suma los de integración contra la base y RabbitMQ de tu compose. Usa una base aparte (`discordia_admin_test`), porque esos tests vacían tablas, así que no toca la que usás para probar a mano.

El CI exige una cobertura mínima del 70 % y corre con PostgreSQL y RabbitMQ. También hay mutation testing de la capa de servicios (`make mutation`, tarda unos segundos y no corre en el CI).

El repositorio se formatea con gofmt. Activá el hook una vez por clon:

```bash
git config core.hooksPath .githooks
```

### Tests de contrato

`openapi.yaml` se genera desde el código (`make openapi`) y se valida con [Schemathesis](https://schemathesis.readthedocs.io): genera casos a partir del contrato, los manda al servicio y verifica que las respuestas (códigos, esquemas, cabeceras) coincidan con lo declarado.

```bash
make contract-deps-up   # Postgres (55501) y RabbitMQ (55511) efímeros en Docker, con migraciones
make contract-test      # levanta el servicio en 18081 y corre Schemathesis
make contract-deps-down
```

El script solo corre contra servicios locales y rechaza cualquier otro host. Usa secretos de juguete y un staff de prueba; no toca ningún entorno real. `CONTRACT_MAX_EXAMPLES` ajusta los casos por operación (25 por defecto). En el CI corre el job `contract`, en paralelo a `test` y sin `secrets`.

Corre dos pasadas. La primera usa ids inventados y cubre los caminos de error. La segunda carga `scripts/contract/seed.sql` en la base efímera y usa esos ids (`scripts/contract/seeded.toml`) para llegar a la lógica real de expedientes, suspensiones y recuperación de cuenta.

## Más documentación

En este repositorio:

- [`docs/historias-de-usuario.md`](docs/historias-de-usuario.md): las historias del catálogo que resuelve este servicio, con sus rutas.
- [`openapi.yaml`](openapi.yaml): contrato de la API REST.
- [`docs/mutation-testing`](docs/mutation-testing/README.md): resultados del mutation testing.

En el repositorio [`docs`](https://github.com/discordia-grupo01/docs):

- [`asyncapi`](https://github.com/discordia-grupo01/docs/tree/main/asyncapi): los eventos que este servicio publica y consume.
