# syntax=docker/dockerfile:1
# ---- build backend ----
FROM golang:1.24-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/starstack ./cmd/starstack

# ---- build frontend ----
FROM node:22-alpine AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- runtime: nginx serves static + proxies API to embedded Go server ----
FROM nginx:1.27-alpine
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=backend /out/starstack /usr/local/bin/starstack
COPY --from=frontend /web/dist /usr/share/nginx/html
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
RUN mkdir -p /app/data /share && chown -R nginx:nginx /app /share
USER nginx
EXPOSE 80
# Go backend listens on :8080 internally (env LISTEN default); nginx :80 proxies /api
CMD ["sh", "-c", "starstack & exec nginx -g 'daemon off;'"]