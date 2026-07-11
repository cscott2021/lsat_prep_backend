FROM --platform=linux/amd64 golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

FROM --platform=linux/amd64 alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/server .

# Run as an unprivileged user rather than root.
RUN addgroup -S app && adduser -S -G app app
USER app

EXPOSE 8080

# Liveness probe against the existing /health endpoint (busybox wget).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://localhost:8080/health || exit 1

CMD ["./server"]
