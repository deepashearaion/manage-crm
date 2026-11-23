# Production image for Coolify (or any Docker host).
#
# Configuration comes from environment variables set in Coolify; no .env file
# is copied into the image. Migrations are embedded in the migrate binary and
# run on every start before the API (set RUN_MIGRATIONS=false to skip).

# ---- build ----
FROM golang:1.26.5-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app

COPY --from=build /out/api /out/migrate /usr/local/bin/

USER app
ENV PORT=8080 \
    RUN_MIGRATIONS=true
EXPOSE 8080

CMD ["sh", "-c", "if [ \"$RUN_MIGRATIONS\" = \"true\" ]; then migrate up || exit 1; fi; exec api"]
