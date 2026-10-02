# syntax=docker/dockerfile:1
# ---- build backend ----
FROM golang:1.26-alpine AS backend
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/starstack ./cmd/starstack

# ---- build frontend ----
FROM node:22-alpine AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

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