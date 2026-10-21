#!/bin/sh
# Validate an isolated source snapshot with disposable Docker resources.
set -eu
cd "$(dirname "$0")/.."
mode=${1:-postgres}
case "$mode" in
 postgres) ;;
 --all) ;;
 *) echo 'Usage: scripts/test-postgres.sh [--all]' >&2; exit 2 ;;
esac
[ "$#" -le 1 ] || { echo 'Too many arguments' >&2; exit 2; }
run_id="$(date +%s)-$$"
echo "Validation run: $run_id"
network="hpts-test-$run_id"
database="hpts-postgres-$run_id"
network_id=
database_id=
go_id=
contract_id=
archive=
cleanup() {
 result=$?
 trap - EXIT INT TERM
 for id in "$go_id" "$contract_id" "$database_id"; do
  if [ -n "$id" ]; then
   if ! docker rm -f "$id" >/dev/null; then
    echo "Failed to remove validation container $id" >&2
    result=1
   fi
  fi
 done
 if [ -n "$network_id" ]; then
  if ! docker network rm "$network_id" >/dev/null; then
   echo "Failed to remove validation network $network_id" >&2
   result=1
  fi
 fi
 if [ -n "$archive" ]; then rm -f "$archive"; fi
 exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Snapshot before creating containers. A tar failure must not be hidden by the
# status of a downstream command in a shell pipeline. Never copy .git or secrets.
archive=$(mktemp "${TMPDIR:-/tmp}/hpts-validation.XXXXXX")
tar -cf "$archive" cmd src tests api migrations scripts Makefile go.mod go.sum
network_id=$(docker network create "$network")
if [ "$mode" = --all ]; then
 echo '== Contract checks =='
 contract_id=$(docker create -i --name "hpts-contract-$run_id" --network "$network" python:3.12-slim sh -ec '
 mkdir /workspace; tar -xf - -C /workspace; cd /workspace
 # Make is used only by the orchestration regression tests in this container.
 apt-get update -qq
 DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends make >/dev/null
 python -m pip install --quiet --disable-pip-version-check --root-user-action=ignore -r tests/contracts/requirements.txt
 python tests/contracts/validate_user_session.py
 python -m unittest discover -s tests/validation -v
 ')
 docker start -ai "$contract_id" < "$archive"
 [ "$(docker inspect -f '{{.State.ExitCode}}' "$contract_id")" = 0 ]
fi
database_id=$(docker create --name "$database" --network "$network" --network-alias postgres \
 --tmpfs /var/lib/postgresql/data \
 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17)
docker start "$database_id" >/dev/null
attempt=0
until docker exec "$database_id" pg_isready -U postgres >/dev/null 2>&1; do
 attempt=$((attempt + 1))
 if [ "$attempt" -ge 60 ]; then echo 'PostgreSQL readiness timed out' >&2; exit 1; fi
 sleep 1
done
echo '== Go checks, builds and PostgreSQL tests =='
go_id=$(docker create -i --name "hpts-go-$run_id" --network "$network" \
 -e VALIDATION_MODE="$mode" \
 -e TEST_DATABASE_URL=postgres://postgres@postgres:5432/postgres?sslmode=disable \
 golang:1.26 sh -ec '
 mkdir /workspace; tar -xf - -C /workspace; cd /workspace
 make check
 if [ "$VALIDATION_MODE" = --all ]; then
  go test -race -count=1 ./...
 fi
 go test -race -tags=integration -count=1 -v ./tests/integration/...
 ')
docker start -ai "$go_id" < "$archive"
[ "$(docker inspect -f '{{.State.ExitCode}}' "$go_id")" = 0 ]
remaining=$(docker exec "$database_id" psql -U postgres -Atc "SELECT count(*) FROM pg_database WHERE datname LIKE 't06_%'")
[ "$remaining" = 0 ] || { echo 'Fixture databases remain after tests' >&2; exit 1; }
echo 'Validation passed; no fixture databases remain. Removing validation resources.'
