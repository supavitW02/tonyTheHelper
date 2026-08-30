# syntax=docker/dockerfile:1

# 1. Build the web app. Vite writes into api/static, which the Go build then embeds.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build the Go binary with the web app inside it (ADR-0003).
FROM golang:1.24-alpine AS api
WORKDIR /src/api
COPY api/ ./
COPY --from=web /src/api/static ./static
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server .

# 3. Ship one small image holding nothing but the binary. distroless/static carries CA
#    certificates, which later tickets need for Supabase and LINE, and runs as nonroot.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
