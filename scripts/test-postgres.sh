#!/bin/sh
# Disposable database server and source-copy runner; no host ports or volumes.
set -eu
cd "$(dirname "$0")/.."
run_id="$(date +%s)-$$"
network="hpts-test-$run_id"
database="hpts-postgres-$run_id"
runner="hpts-go-$run_id"
cleanup() {
 docker rm -f "$runner" "$database" >/dev/null 2>&1 || true
 docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker network create "$network" >/dev/null
docker run -d --name "$database" --network "$network" --network-alias postgres \
 --tmpfs /var/lib/postgresql/data \
 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17 >/dev/null
attempt=0
until docker exec "$database" pg_isready -U postgres >/dev/null 2>&1; do
 attempt=$((attempt + 1))
 if [ "$attempt" -ge 60 ]; then echo 'PostgreSQL readiness timed out' >&2; exit 1; fi
 sleep 1
done
# stdin transports sources to Docker Desktop without host path sharing.
tar -cf - cmd src tests migrations Makefile go.mod go.sum |
 docker run --rm -i --name "$runner" --network "$network" \
 -e TEST_DATABASE_URL=postgres://postgres@postgres:5432/postgres?sslmode=disable \
 golang:1.26 sh -ec '
 mkdir /workspace; tar -xf - -C /workspace; cd /workspace
 make check
 go test -race -tags=integration -count=1 -v ./tests/integration/...
 '
# Check the harness left no test databases on the dedicated server.
remaining=$(docker exec "$database" psql -U postgres -Atc "SELECT count(*) FROM pg_database WHERE datname LIKE 't06_%'")
[ "$remaining" = 0 ] || { echo 'Fixture databases remain after tests' >&2; exit 1; }
echo 'PostgreSQL checks passed; no fixture databases remain.'
