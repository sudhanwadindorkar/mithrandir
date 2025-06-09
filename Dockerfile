# Build stage
FROM golang:1.24.4-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o proxy main.go

# Runtime stage
FROM alpine:latest
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
WORKDIR /proxy
COPY --from=builder /app/proxy .

# Run as non-root user
USER appuser

EXPOSE 8080
ENTRYPOINT ["./proxy"]