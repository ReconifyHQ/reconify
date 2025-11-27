FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY cli/go.mod cli/go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY cli/ ./

# Build binary
ARG VERSION=dev
ARG BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o reconify ./cmd/reconify

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/reconify /usr/local/bin/reconify

RUN chmod +x /usr/local/bin/reconify

ENTRYPOINT ["reconify"]
