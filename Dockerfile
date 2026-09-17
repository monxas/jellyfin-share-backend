# =============================================================================
# PRODUCTION DOCKERFILE
# Multi-stage build for minimal final image
# =============================================================================

# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder

WORKDIR /app/web

# Install dependencies first (cache layer)
COPY web/package*.json ./
RUN npm ci --production=false

# Build frontend
COPY web/ ./
RUN npm run build

# Stage 2: Build backend
FROM golang:1.23-alpine AS backend-builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Install dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Copy built frontend for embedding
COPY --from=frontend-builder /app/web/dist ./web/dist

# Build optimized binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=$(date +%Y%m%d)" \
    -o /jfshare ./cmd/server

# Stage 3: Final minimal image
FROM alpine:3.20

LABEL org.opencontainers.image.title="JFShare"
LABEL org.opencontainers.image.description="Jellyfin one-time share link system"
LABEL org.opencontainers.image.source="https://github.com/jellyfin-share/jellyfin-share-backend"

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=backend-builder /jfshare .

# Copy migrations for embedded FS
COPY --from=backend-builder /app/migrations ./migrations

# Copy frontend dist for embedded FS
COPY --from=frontend-builder /app/web/dist ./web/dist

# Create non-root user
RUN addgroup -g 1000 jfshare && \
    adduser -D -u 1000 -G jfshare jfshare && \
    chown -R jfshare:jfshare /app

USER jfshare

EXPOSE 8080

# Follow JFSHARE_PORT rather than hard-coding a port: the compose files set 8097,
# and a fixed 8080 here reports the container unhealthy while it is serving fine.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider "http://localhost:${JFSHARE_PORT:-8080}/health" || exit 1

ENTRYPOINT ["/app/jfshare"]
