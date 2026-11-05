.PHONY: run test test-integration build migrate seed-staff republish-suspensions

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

build:
	go build -o bin/api ./cmd/api

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
