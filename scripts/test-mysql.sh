#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
command -v docker >/dev/null 2>&1 || { echo 'Docker com Compose é necessário para este teste.' >&2; exit 1; }
trap 'docker compose -p aluguel-go-integration -f compose.test.yml down' EXIT INT TERM
docker compose -p aluguel-go-integration -f compose.test.yml up -d --wait
ALUGUEL_TEST_DSN='root:integration-only@tcp(127.0.0.1:13306)/aluguel_test?parseTime=true' go test -tags=integration -count=1 ./internal/app
