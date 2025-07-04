GO=go
GO_FILES=$(shell find . -name *.go)
BINARIES=rootlesskit rootlessctl rootlesskit-docker-proxy

.PHONY: all
all: $(addprefix bin/, $(BINARIES))

.PHONY: clean
clean:
	$(RM) -r bin/ _artifact/

bin/rootlesskit: $(GO_FILES)
	$(GO) build -mod=vendor -o $@ -v ./cmd/rootlesskit

bin/rootlessctl: $(GO_FILES)
	$(GO) build -mod=vendor -o $@ -v ./cmd/rootlessctl

bin/rootlesskit-docker-proxy: $(GO_FILES)
	@echo "NOTE: rootlesskit-docker-proxy is required only if you use Docker prior to v28."
	@echo "NOTE: rootlesskit-docker-proxy is DEPRECATED and will be removed in RootlessKit v3."
	$(GO) build -mod=vendor -o $@ -v ./cmd/rootlesskit-docker-proxy

.PHONY: cross
cross:
	./hack/make-cross.sh

BINDIR ?= /usr/local/bin
.PHONY: install
install:
	install -D -m 755 $(CURDIR)/bin/rootlesskit $(DESTDIR)$(BINDIR)/rootlesskit
	install -D -m 755 $(CURDIR)/bin/rootlessctl $(DESTDIR)$(BINDIR)/rootlessctl
	install -D -m 755 $(CURDIR)/bin/rootlesskit-docker-proxy $(DESTDIR)$(BINDIR)/rootlesskit-docker-proxy

.PHONY: vendor
vendor:
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify

.PHONY: verify-dependencies
verify-dependencies: vendor
	@test -z "$$(git status --porcelain -- go.mod go.sum vendor/)" \
		|| (echo -e "git status:\n $$(git status -- go.mod go.sum vendor/)\nerror: vendor/, go.mod and/or go.sum not up to date. Run \"make vendor\" to update"; exit 1) \
		&& echo "all vendor files are up to date."
