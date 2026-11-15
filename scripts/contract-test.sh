#!/usr/bin/env bash
# Valida que admin cumpla su openapi.yaml con Schemathesis.
# Levanta el servicio contra un Postgres y un RabbitMQ LOCALES y efimeros,
# nunca contra servicios remotos. Uso: make contract-test
set -euo pipefail

cd "$(dirname "$0")/.."

# Puertos de la tabla del plan de contratos (admin).
DB_HOST="${CONTRACT_DB_HOST:-localhost}"
DB_PORT="${CONTRACT_DB_PORT:-55501}"
MQ_HOST="${CONTRACT_MQ_HOST:-localhost}"
MQ_PORT="${CONTRACT_MQ_PORT:-55511}"
APP_PORT="${CONTRACT_APP_PORT:-18081}"
MAX_EXAMPLES="${CONTRACT_MAX_EXAMPLES:-25}"

# Valores de juguete: no son secretos reales.
JWT_SECRET="contract-test-secret"
STAFF_ID="00000000-0000-4000-8000-0000000000a1"

# Guard: se rechaza todo host que no sea local.
for host in "$DB_HOST" "$MQ_HOST"; do
  case "$host" in
    localhost | 127.0.0.1 | ::1) ;;
    *)
      echo "contract-test: host no local '$host'; este script solo corre contra servicios locales." >&2
      exit 1
      ;;
  esac
done

PY="${PYTHON:-python}"
VENV=".contract-venv"
if [ -x "$VENV/bin/schemathesis" ]; then
  ST="$VENV/bin/schemathesis"
elif [ -x "$VENV/Scripts/schemathesis.exe" ]; then
  ST="$VENV/Scripts/schemathesis.exe"
elif command -v schemathesis >/dev/null 2>&1; then
  ST="schemathesis"
else
  "$PY" -m venv "$VENV"
  if [ -x "$VENV/bin/pip" ]; then PIP="$VENV/bin/pip"; else PIP="$VENV/Scripts/pip.exe"; fi
  "$PIP" install -q "schemathesis==4.33.4"
  if [ -x "$VENV/bin/schemathesis" ]; then ST="$VENV/bin/schemathesis"; else ST="$VENV/Scripts/schemathesis.exe"; fi
fi

TOKEN="$("$PY" scripts/contract/make_token.py "$STAFF_ID" "$JWT_SECRET" 3600)"

LOG="$(mktemp)"
export ENVIRONMENT=development HOST=127.0.0.1 PORT="$APP_PORT"
export DATABASE_HOST="$DB_HOST" DATABASE_PORT="$DB_PORT" DATABASE_USER=discordia DATABASE_PASSWORD=discordia
export DATABASE_NAME=discordia_admin_contract DATABASE_SSLMODE=disable DATABASE_SCHEMA=
export RABBITMQ_URL="amqp://guest:guest@$MQ_HOST:$MQ_PORT/"
export JWT_SECRET ADMIN_EVENTS_HMAC_SECRET="contract-test-hmac" STAFF_USER_IDS="$STAFF_ID"

go build -o .contract-api ./cmd/api
./.contract-api >"$LOG" 2>&1 &
APP_PID=$!
cleanup() {
  kill "$APP_PID" 2>/dev/null || true
  rm -f .contract-api .contract-api.exe "$LOG"
}
trap cleanup EXIT

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$APP_PORT/livez" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$APP_PID" 2>/dev/null; then
    echo "contract-test: el servicio no arrancó:" >&2
    cat "$LOG" >&2
    exit 1
  fi
  sleep 1
done

"$ST" run openapi.yaml \
  --url "http://127.0.0.1:$APP_PORT" \
  -H "Authorization: Bearer $TOKEN" \
  --max-examples "$MAX_EXAMPLES" \
  --checks all
