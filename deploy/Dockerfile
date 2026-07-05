# Multi-stage, multi-arch build. Frontend first, then a static Go binary.
FROM --platform=$BUILDPLATFORM node:20-alpine AS webbuild
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build

# Keep the Go tag in step with go.mod, or go mod download refuses.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS gobuild
ARG TARGETOS TARGETARCH TARGETVARIANT VERSION=docker
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webbuild /src/web/dist ./web/dist
# GOARM comes from the platform variant (linux/arm/v7 → 7); it is ignored
# by every arch except 32-bit arm.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -ldflags "-s -w -X main.version=$VERSION" -o /minos ./cmd/minos

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/DanDreadless/minos" \
      org.opencontainers.image.description="Minos - every query gets judged. A modern DNS sinkhole." \
      org.opencontainers.image.licenses="GPL-3.0"
COPY --from=gobuild /minos /minos
COPY --from=gobuild /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# DNS in/out, API
EXPOSE 53/udp 53/tcp 8080/tcp
VOLUME ["/data"]
ENTRYPOINT ["/minos", "serve", "-config", "/data/minos.yaml"]
