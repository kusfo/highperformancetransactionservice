GO ?= go
SERVICES := user account transaction transaction-worker
BINARIES := $(addprefix bin/,$(SERVICES))
GO_FILES := $(shell find cmd src -type f -name '*.go' 2>/dev/null)

.PHONY: build fmt fmt-check test vet check clean

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
