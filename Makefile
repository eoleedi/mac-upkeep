.PHONY: build test install

build:
	go build -o bin/ua ./cmd/ua

test:
	go test -race ./...
	go vet ./...

install: build
	install -d "$(HOME)/.local/bin" "$(HOME)/.config/ua"
	install -m 0755 bin/ua "$(HOME)/.local/bin/ua"
	@test -e "$(HOME)/.config/ua/config.toml" || install -m 0644 config.example.toml "$(HOME)/.config/ua/config.toml"
