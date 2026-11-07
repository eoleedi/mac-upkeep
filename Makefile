.PHONY: build test test-install install-files install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/mac-upkeep ./cmd/mac-upkeep

test:
	go test -race ./...
	go vet ./...
	$(MAKE) test-install

test-install: build
	sh scripts/test-install.sh

install-files:
	test -x bin/mac-upkeep
	install -d "$(HOME)/.local/bin" "$(HOME)/.config/mac-upkeep"
	install -m 0755 bin/mac-upkeep "$(HOME)/.local/bin/mac-upkeep"
	@test -e "$(HOME)/.config/mac-upkeep/config.toml" || { \
		if test -e "$(HOME)/.config/ua/config.toml"; then \
			install -m 0644 "$(HOME)/.config/ua/config.toml" "$(HOME)/.config/mac-upkeep/config.toml"; \
		else \
			install -m 0644 config.example.toml "$(HOME)/.config/mac-upkeep/config.toml"; \
		fi; \
	}

install: build install-files
