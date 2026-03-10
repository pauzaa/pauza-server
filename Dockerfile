# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /pauza-server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /pauza-migrate ./cmd/migrate

# Runtime stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates wget

RUN addgroup -S appgroup && adduser -S appuser -G appgroup
RUN mkdir -p /var/lib/pauza/photos && chown -R appuser:appgroup /var/lib/pauza

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /pauza-server .
COPY --from=builder --chown=appuser:appgroup /pauza-migrate .

USER appuser

EXPOSE 8080

CMD ["./pauza-server"]
