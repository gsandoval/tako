# Tako — build and install the CLI from a checkout.
#
# `go install` puts binaries in $GOBIN, falling back to $GOPATH/bin
# (~/go/bin by default). `make where` prints the resolved directory —
# it has to be on your PATH for `tako` to be runnable by name.

GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: help build install uninstall test where

help: ## list the targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk -F':.*?## ' '{printf "  %-10s %s\n", $$1, $$2}'

build: ## build ./tako in the repo root (gitignored, not installed)
	go build -o tako ./cmd/tako

install: ## install the tako binary (into the dir `make where` prints)
	go install ./cmd/tako
	@echo "installed $(GOBIN)/tako"
	@command -v tako >/dev/null 2>&1 || \
		echo "note: $(GOBIN) is not on your PATH — add it to run 'tako' by name"

uninstall: ## remove the installed binary
	rm -f "$(GOBIN)/tako"
	@echo "removed $(GOBIN)/tako"

test: ## unit + golden + conformance(local)
	go test ./...

where: ## print where install would put the binary
	@echo "$(GOBIN)/tako"
