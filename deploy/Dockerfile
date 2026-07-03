# Multi-stage, multi-arch build. Frontend first, then a static Go binary.
FROM --platform=$BUILDPLATFORM node:20-alpine AS webbuild
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS gobuild
ARG TARGETOS TARGETARCH VERSION=docker
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webbuild /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-s -w -X main.version=$VERSION" -o /minos ./cmd/minos

FROM scratch
COPY --from=gobuild /minos /minos
COPY --from=gobuild /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# DNS in/out, API
EXPOSE 53/udp 53/tcp 8080/tcp
VOLUME ["/data"]
ENTRYPOINT ["/minos", "serve", "-config", "/data/minos.yaml"]
