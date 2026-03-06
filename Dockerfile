# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /pauza-server ./cmd/server

# Runtime stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates wget

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /pauza-server .

USER appuser

EXPOSE 8080

CMD ["./pauza-server"]
