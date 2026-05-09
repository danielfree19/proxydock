# syntax=docker/dockerfile:1.7

# 1. Build the web UI with Node.
#    The Compose build context is apps/api/, so apps/web/ is reached by
#    making the docker build include the parent dir — see compose.yaml,
#    which sets context: ../../ and dockerfile: apps/api/Dockerfile.
FROM node:20-alpine AS web-build
WORKDIR /web
COPY apps/web/package.json apps/web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY apps/web/ ./
RUN npm run build

# 2. Build the Go binary, bundling the freshly-built web assets via embed.
FROM golang:1.25-alpine AS api-build
WORKDIR /src

COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download

COPY apps/api/ ./

# Replace the placeholder dist/ with the real Vite output before
# compiling — the //go:embed directive picks it up from disk at build time.
RUN rm -rf internal/webui/dist
COPY --from=web-build /web/dist internal/webui/dist

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/manager-api ./cmd/manager-api

# 3. Tiny final image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api-build /out/manager-api /usr/local/bin/manager-api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/manager-api"]
