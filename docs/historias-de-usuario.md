# Historias de usuario

Las historias del catálogo de la cátedra que resuelve este servicio, con su número, y las que resuelve junto con otros servicios. Las rutas completas con sus parámetros y respuestas están en [`openapi.yaml`](../openapi.yaml). Todas exigen el JWT de un usuario del staff.

## Obligatorias

| # | Historia | Puntos | Rutas |
|---|---|---|---|
| 29 | Suspender usuario a nivel plataforma | 3 | `POST /v1/admin/users/{id}/suspension` |
| 30 | Reactivar usuario suspendido | 1 | `DELETE /v1/admin/users/{id}/suspension` |
| 31 | Ver listado de usuarios con filtros | 2 | `GET /v1/admin/users` |
| 32 | Ver listado de servidores de la plataforma | 2 | `GET /v1/admin/servers` |

## Optativas

| # | Historia | Puntos | Rutas |
|---|---|---|---|
| 68 | Eliminar servidor desde el backoffice | 5 | `DELETE /v1/admin/servers/{id}` |
| 69 | Suspender servidor completo | 5 | `POST /v1/admin/servers/{id}/suspension`, `DELETE /v1/admin/servers/{id}/suspension` |
| 81 | Ver expediente de investigación de usuario o servidor | 5 | `GET /v1/admin/users/{id}/dossier`, `GET /v1/admin/servers/{id}/dossier` y sus rutas por sección |

## Historias en las que participa

Estas historias las completan otros servicios. Acá está la parte que le toca a `admin`:

| # | Historia | Qué hace este servicio |
|---|---|---|
| 35 | Autenticación de dos factores (2FA) | Muestra al staff los pedidos de recuperación de cuenta de quien perdió el segundo factor y los aprueba o rechaza: `GET /v1/admin/recovery-requests` y las rutas `approval` y `rejection`. |
| 62 | Reportar mensaje o usuario | Recibe los reportes de `servers` para armar el expediente. |
| 29, 30, 69 | Suspensiones | Publica los eventos firmados que `identify-service`, `servers`, `messaging` y `voice-sfu` consumen para bloquear. |

## Decisiones técnicas

- **ADR 0004, outbox:** el mismo patrón que usa `messaging`, para que el evento no se pierda si RabbitMQ se cae.
