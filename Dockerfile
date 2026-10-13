# Multi-stage Dockerfile for EasyHR Go API (Optimized for Google Cloud Run)

# Stage 1: Build the Go binary
FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compile static Linux binary without CGO
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o easyhr-api cmd/api/main.go

# Stage 2: Minimal runtime image
FROM alpine:3.21

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

# Copy binary and database migrations
COPY --from=builder /app/easyhr-api /app/easyhr-api
COPY --from=builder /app/db/migrations /app/db/migrations

# Default Cloud Run port is 8080
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/easyhr-api"]
