ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

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

FROM alpine:3.23

# git + ca-certificates are required at runtime; tini reaps orphaned processes
# (see ENTRYPOINT); bash, curl and nano are added so the container can be
# exec'd into for basic debugging (a shell, an HTTP client, and an editor).
RUN apk add --no-cache git ca-certificates tini bash curl nano \
    && test -x /sbin/tini \
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

# tini runs as PID 1 so that orphaned processes get reaped. git spawns helpers
# (git-remote-https, ssh, credential helpers) that can outlive their parent git
# process; once orphaned they are reparented to PID 1, and a Go binary as PID 1
# never wait()s for them — they accumulate as zombies until the container hits
# its PID limit and git can no longer fork. tini also forwards signals, so
# graceful shutdown is unaffected.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
