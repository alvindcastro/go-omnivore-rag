# ── Stage 1: Builder ─────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Download dependencies first — layer is cached until go.mod/go.sum change
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Generate Swagger docs (docs/ is gitignored, must be created here)
# Uses the exact version pinned in the go:generate directive in handlers.go
RUN go run github.com/swaggo/swag/cmd/swag@v1.16.6 \
    init -g cmd/main.go -d . -o docs

# Build HTTP server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /dist/omnivore-http ./cmd/main.go

# Build gRPC server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /dist/omnivore-grpc ./cmd/grpc/main.go


# ── Stage 2: Final image ──────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates: required for HTTPS calls to Azure (OpenAI, Search, Blob)
# wget: used by the HEALTHCHECK
RUN apk add --no-cache ca-certificates wget

# Non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Binaries
COPY --from=builder /dist/omnivore-http  ./omnivore-http
COPY --from=builder /dist/omnivore-grpc  ./omnivore-grpc

# Generated Swagger docs (served at /docs by the HTTP server)
COPY --from=builder /build/docs ./docs

# Documents are mounted here at runtime — directory must exist
RUN mkdir -p data/docs/banner data/docs/sop

RUN chown -R appuser:appgroup /app
USER appuser

EXPOSE 8000
EXPOSE 9000

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8000/health || exit 1

# Default: HTTP server. Override with ./omnivore-grpc for the gRPC container.
CMD ["./omnivore-http"]
