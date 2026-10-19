# syntax=docker/dockerfile:1
# ---- build backend ----
FROM golang:1.26-alpine AS backend
ARG VERSION=dev
# Use a reachable Go module proxy (default proxy.golang.org is flaky here).
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/starstack ./cmd/starstack

# ---- build frontend ----
FROM node:22-alpine AS frontend
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund --registry=${NPM_REGISTRY}
COPY web/ ./
RUN --mount=type=cache,target=/root/.npm \
    npm run build

# ---- runtime: nginx serves static frontend and proxies /api to the Go API ----
FROM nginx:1.27-alpine
RUN apk add --no-cache ffmpeg ca-certificates tzdata

COPY --from=backend /out/starstack /usr/local/bin/starstack
COPY --from=frontend /web/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Runtime runs as root so bind-mounted host directories (which docker may
# create as root-owned) are writable out of the box. For a private host this
# is the pragmatic default; re-harden with a non-root user if you pin host
# dir ownership accordingly.
RUN mkdir -p /app/data /share /tmp/nginx && chmod +x /usr/local/bin/entrypoint.sh

# Internal API listen port (nginx reverse-proxies to this). Overridable by env.
ENV LISTEN=:8080 \
    DATA_DIR=/app/data \
    SHARE_ROOT=/share

EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -q -O - http://127.0.0.1/api/health || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
