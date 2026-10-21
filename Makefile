GO ?= go
SERVICES := user account transaction transaction-worker migrate
BINARIES := $(addprefix bin/,$(SERVICES))
GO_FILES := $(shell find cmd src -type f -name '*.go' 2>/dev/null)

.PHONY: build fmt fmt-check test vet check clean migrate test-integration test-postgres check-all

build: $(BINARIES)

$(BINARIES): bin/%: $(GO_FILES) go.mod
	mkdir -p bin
	$(GO) build -trimpath -o $@ ./cmd/$*

fmt:
	$(GO) fmt ./...

fmt-check:
	@formatted=$$($(GO) fmt ./...) || exit $$?; test -z "$$formatted" || { echo "Go formatting changed files; run make fmt and review changes"; exit 1; }

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

check: fmt-check vet test build

clean:
	rm -f $(BINARIES)

# Integration tests require a disposable server with CREATE/DROP DATABASE rights.
test-integration:
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required"; exit 1; }
	$(GO) test -race -tags=integration -count=1 ./tests/integration/...

test-postgres:
	./scripts/test-postgres.sh

migrate: bin/migrate
	./bin/migrate

# Complete isolated validation; requires Docker, not host Go/Python/PostgreSQL.
check-all:
	./scripts/test-postgres.sh --all
