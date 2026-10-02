# syntax=docker/dockerfile:1
# ---- build backend ----
FROM golang:1.24-alpine AS backend
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
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh

# Run as a non-root user owned directories.
RUN mkdir -p /app/data /share \
    && chown -R nginx:nginx /app /share \
    && chmod +x /usr/local/bin/entrypoint.sh

# Internal API listen port (nginx reverse-proxies to this). Overridable by env.
ENV LISTEN=:8080 \
    DATA_DIR=/app/data \
    SHARE_ROOT=/share

USER nginx
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -q -O - http://127.0.0.1/api/health || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]