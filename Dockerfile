# Build stage
FROM golang:1.24-bullseye AS builder

WORKDIR /app

# Copy all source code (including vendor/)
COPY . .

# Build binary with vendored dependencies (no network needed)
RUN go build -mod=vendor -o bin/pipeline .

# Runtime stage
FROM golang:1.24-alpine

# Install runtime dependencies
# gcompat: For Go binaries built on Debian (https://github.com/golang/go/issues/59305)
# ca-certificates: For HTTPS/TLS connections
RUN apk add --no-cache gcompat ca-certificates curl

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/bin/pipeline .

# Create directory for mounted certificates (TrustMed mTLS)
RUN mkdir -p /etc/creds/trustmed

# Create directory for local certificate fallback
RUN mkdir -p /root/certs/trustmed

# Expose HTTP port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

# Run the pipeline service
CMD ["./pipeline"]
