FROM golang:1.23-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/atm-backend ./main.go

FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl \
    && addgroup -S app \
    && adduser -S app -G app \
    && mkdir -p /app/uploads \
    && chown -R app:app /app

COPY --from=builder /out/atm-backend /app/atm-backend

ENV API_PORT=8080
ENV GIN_MODE=release

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${API_PORT}/" >/dev/null || exit 1

USER app

CMD ["/app/atm-backend"]
