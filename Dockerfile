ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/gypsum ./cmd/wiki

FROM alpine:3.21

RUN apk add --no-cache git ca-certificates \
    && addgroup -g 1000 -S app \
    && adduser -u 1000 -S app -G app
WORKDIR /app

COPY --from=builder /out/gypsum /app/gypsum
COPY web /app/web
COPY cmd/wiki/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod +x /app/docker-entrypoint.sh && mkdir -p /app/data/pages && chown -R app:app /app

USER app
EXPOSE 8080 9091

ENTRYPOINT ["/app/docker-entrypoint.sh"]
