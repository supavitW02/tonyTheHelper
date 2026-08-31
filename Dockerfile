# syntax=docker/dockerfile:1

# 1. Build the web app. Vite writes into api/static, which the Go build then embeds.
FROM node:24-alpine AS web
# The build stamps this into the bundle and into version.json, which is how a running app
# notices a newer deploy. The build context has no .git, so it is passed in rather than
# read from the repository; without it the build falls back to a timestamp.
ARG BUILD_ID=""
ENV BUILD_ID=$BUILD_ID
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 2. Build the Go binary with the web app inside it (ADR-0003).
FROM golang:1.25-alpine AS api
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
