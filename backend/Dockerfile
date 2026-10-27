# one dockerfile, two images: "api" and "worker" (the worker image also
# carries the seed tool). build with: docker build --target api backend/

# ---- build stage: compile static go binaries ----
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
COPY . .
# tidy fills in go.sum if it is missing; commit go.sum to make builds repeatable
RUN go mod tidy && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

# ---- shared runtime base: tiny image, non-root user ----
FROM alpine:3.20 AS runtime
RUN adduser -D -u 10001 app && apk add --no-cache ca-certificates
USER app
WORKDIR /app

# ---- worker image (also used to run the seed tool) ----
FROM runtime AS worker
COPY --from=build /out/worker /out/seed /app/
ENTRYPOINT ["/app/worker"]

# ---- api image ----
FROM runtime AS api
COPY --from=build /out/api /app/api
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=2s --retries=10 CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/app/api"]
