# Minos build targets. See CLAUDE.md for the full command reference.

VERSION ?= 0.1.0-dev
LDFLAGS  = -s -w -X main.version=$(VERSION)

.PHONY: dev build build-arm64 web test lint docker clean

dev:
	cd web && npm run dev &
	air

web:
	cd web && npm install --no-fund --no-audit && npm run build

build: web
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/minos ./cmd/minos

build-arm64: web
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/minos-linux-arm64 ./cmd/minos

test:
	go test ./... -race

lint:
	golangci-lint run
	cd web && npm run check

bench:
	go test ./internal/filter -bench . -benchmem -run '^$$'

docker:
	docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile -t minos:$(VERSION) .

clean:
	rm -rf bin web/dist web/node_modules
