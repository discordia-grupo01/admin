# Mutation testing de admin

## Qué es

La cobertura dice qué líneas **se ejecutan** durante los tests, pero no si los
tests **revisan** lo que pasa en ellas. El mutation testing mete a propósito
errores chicos en el código, uno por vez (cambia `>` por `>=`, niega un `if`,
cambia `||` por `&&`...), y corre los tests con cada uno. Si algún test falla,
el error quedó **detectado** (`KILLED`); si todos pasan, **sobrevivió**
(`LIVED`). La **efectividad** es detectados / (detectados + sobrevivientes).

Usamos [gremlins](https://github.com/go-gremlins/gremlins), para Go, sobre
`internal/service` (listados del backoffice, moderación de servidores y
expediente de investigación), con todos los mutantes opcionales activados.

## Cómo correrlo

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
make mutation
```

Tarda unos segundos y no está en el CI. Los tests de `internal/service` son
unitarios: no usan base de datos ni RabbitMQ.

## Resultados

Archivos `before.*`.

| | Resultado |
|---|---|
| Detectados | 62 |
| Sobrevivientes | 2 |
| No cubiertos | 6 |
| Timeout | 0 |
| **Efectividad** | **96,88%** |

No hizo falta agregar tests: los 2 sobrevivientes son **equivalentes**. Los
dos están en `activity_pruner.go:49` (`if n > 0`), que solo decide si se
escribe en el log cuántos registros viejos se borraron. Cambiar la condición
no altera nada de lo que hace el servicio, así que no hay test posible.

Los 6 no cubiertos sí son código sin tests: 4 en `staff_service.go` (el alta
de miembros del staff que usa `make seed-staff`) y 2 constructores
(`NewActivityService`, `NewDossierSyncService`). gremlins no prueba código que
ningún test ejecuta, así que el 96,88% dice que lo testeado está bien testeado,
no que todo el servicio tenga tests. El alta de staff es lo que conviene
cubrir si se suma un test.
