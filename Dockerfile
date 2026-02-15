FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gypsum ./cmd/wiki

FROM alpine:3.21

RUN addgroup -S app && adduser -S app -G app
WORKDIR /app

COPY --from=builder /out/gypsum /app/gypsum
COPY web /app/web

RUN mkdir -p /app/data/pages /app/data/secure && chown -R app:app /app

USER app
EXPOSE 8080

ENTRYPOINT ["/app/gypsum"]
