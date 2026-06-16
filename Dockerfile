ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

ARG TARGETOS TARGETARCH
# -s -w strips symbol table and DWARF; -trimpath drops build-host paths.
# Shaves ~30 % off binary size with no runtime impact.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/gypsum ./cmd/wiki

FROM alpine:3.24

RUN apk add --no-cache git ca-certificates \
    && addgroup -g 1000 -S app \
    && adduser -u 1000 -S app -G app
WORKDIR /app

COPY --from=builder /out/gypsum /usr/local/bin/gypsum
COPY docs /app/docs
COPY cmd/wiki/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# Re-encrypting stored {{secure_aes:...}} fields after rotating the
# encryption passphrase is available as `gypsum re-encrypt -dir ... -old-key
# ... -new-key ...` (see docs).
RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
    && mkdir -p /app/data/pages && chown -R app:app /app

USER app
EXPOSE 8080 9091

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
