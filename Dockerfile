FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gypsum ./cmd/wiki

FROM alpine:3.21

RUN apk add --no-cache git ca-certificates && addgroup -S app && adduser -S app -G app
WORKDIR /app

COPY --from=builder /out/gypsum /app/gypsum
COPY web /app/web
COPY cmd/wiki/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && mkdir -p /app/data/pages /app/data/secure && chown -R app:app /app

USER app
EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
