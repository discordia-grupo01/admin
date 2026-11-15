.PHONY: run test test-integration mutation build migrate openapi contract-deps-up contract-deps-down contract-test seed-staff republish-suspensions voice-dlq-check voice-dlq-replay

-include .env
export

DATABASE_HOST ?= localhost
DATABASE_PORT ?= 5433
DATABASE_USER ?= discordia
DATABASE_PASSWORD ?= discordia
DATABASE_NAME ?= discordia_admin
DATABASE_SSLMODE ?= disable

MIGRATE = go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

run:
	go run ./cmd/api

# Tests unitarios. Los de integración se saltean solos si no hay servicios.
test:
	go test ./... -coverpkg=./internal/... -cover

# Mutation testing de internal/service con gremlins (ver docs/mutation-testing).
# Tarda unos segundos; no corre en el CI.
mutation:
	go clean -testcache
	gremlins unleash ./internal/service --timeout-coefficient 30 --workers 4 \
		--invert-assignments --invert-logical --invert-loopctrl --remove-self-assignments \
		--invert-bitwise --invert-bwassign --output docs/mutation-testing/before.json

build:
	go build -o bin/api ./cmd/api

# Genera openapi.yaml desde las anotaciones swag del codigo (mismo flujo que servers)
# y valida la sintaxis del resultado. --requiredByDefault: todos los campos de las
# respuestas se devuelven siempre (los opcionales van como null, nunca ausentes).
openapi:
	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g cmd/api/main.go -o ./.openapi-tmp --parseInternal --requiredByDefault -q
	npx --yes swagger2openapi@7 ./.openapi-tmp/swagger.yaml -o openapi.yaml --patch
	npx --yes swagger-cli@4.0.4 validate openapi.yaml

# Tests de contrato: Schemathesis contra el servicio levantado con un Postgres y
# un RabbitMQ efimeros y locales (puertos 55501 y 55511). Ver README.
contract-deps-up:
	docker run -d --rm --name contract-admin-pg -p 55501:5432 -e POSTGRES_USER=discordia -e POSTGRES_PASSWORD=discordia -e POSTGRES_DB=discordia_admin_contract postgres:16-alpine
	docker run -d --rm --name contract-admin-rabbitmq -p 55511:5672 rabbitmq:3.13-alpine
	until docker exec contract-admin-pg pg_isready -U discordia >/dev/null 2>&1; do sleep 1; done
	sleep 2
	$(MAKE) migrate DATABASE_HOST=localhost DATABASE_PORT=55501 DATABASE_NAME=discordia_admin_contract DATABASE_SCHEMA=

contract-deps-down:
	-docker rm -f contract-admin-pg contract-admin-rabbitmq

contract-test:
	bash scripts/contract-test.sh

# Aplica las migraciones pendientes. Lee la conexión de .env, así el mismo
# target sirve para el Postgres local (defaults de arriba) y para Supabase
# (host, DATABASE_SSLMODE=require y DATABASE_SCHEMA en .env). Si la password
# tiene caracteres especiales (@, :, /, #) hay que codificarla en la URL.
migrate:
	$(MIGRATE) -path migrations -database "postgres://$(DATABASE_USER):$(DATABASE_PASSWORD)@$(DATABASE_HOST):$(DATABASE_PORT)/$(DATABASE_NAME)?sslmode=$(DATABASE_SSLMODE)$(if $(DATABASE_SCHEMA),&search_path=$(DATABASE_SCHEMA),)" up

# Da de alta como staff al usuario con ese email (tiene que estar en la réplica
# de usuarios). Uso: make seed-staff EMAIL=manu@example.com
seed-staff:
	@test -n "$(EMAIL)" || (echo "falta EMAIL: make seed-staff EMAIL=manu@example.com" >&2; exit 1)
	go run ./cmd/seedstaff --email "$(EMAIL)"

# Vuelve a publicar un admin.user_suspended por cada usuario suspendido y un
# admin.server_suspended por cada servidor suspendido, para poner al
# día a un consumidor nuevo. Uso: make republish-suspensions [ARGS=--dry-run]
republish-suspensions:
	go run ./cmd/republishsuspensions $(ARGS)

# Inspecciona cada DLQ indicada en VOICE_DLQ_QUEUES y devuelve código 2 si
# alguna supera VOICE_DLQ_THRESHOLD (por defecto 0), para conectarlo a una
# alarma de cron/systemd/New Relic.
voice-dlq-check:
	go run ./cmd/voicedlq --threshold "$(or $(VOICE_DLQ_THRESHOLD),0)"

# Replay explícito y acotado. Uso: make voice-dlq-replay ARGS="--limit 100"
voice-dlq-replay:
	go run ./cmd/voicedlq --replay $(ARGS)

# Corre también los tests de integración contra el Postgres y el RabbitMQ de
# docker-compose (`docker compose up -d db rabbitmq`). Usa una base aparte,
# discordia_admin_test, porque esos tests vacían tablas.
test-integration:
	docker compose exec -T db psql -U discordia -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='discordia_admin_test'" | grep -q 1 \
		|| docker compose exec -T db createdb -U discordia discordia_admin_test
	$(MAKE) migrate DATABASE_NAME=discordia_admin_test DATABASE_SCHEMA=
	DATABASE_HOST=$(DATABASE_HOST) DATABASE_PORT=$(DATABASE_PORT) DATABASE_USER=$(DATABASE_USER) \
	DATABASE_PASSWORD=$(DATABASE_PASSWORD) DATABASE_NAME=discordia_admin_test DATABASE_SSLMODE=$(DATABASE_SSLMODE) \
	DATABASE_SCHEMA= RABBITMQ_URL=amqp://guest:guest@localhost:5672/ \
	go test ./... -count=1 -coverpkg=./internal/... -cover
