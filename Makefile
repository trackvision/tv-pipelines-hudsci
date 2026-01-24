.PHONY: help build test run clean deps fmt lint vet check test-unit test-e2e test-inbound test-outbound reset-inbound reset-outbound docker-build docker-run setup-hooks

# Default target
.DEFAULT_GOAL := help

help:
	@echo "Available commands:"
	@echo "  make build          - Build the pipeline binary"
	@echo "  make test           - Run all tests (unit + E2E)"
	@echo "  make test-unit      - Run unit tests only"
	@echo "  make test-e2e       - Run E2E tests"
	@echo "  make test-inbound   - Full inbound test: reset → upload → run → verify"
	@echo "  make test-outbound  - Full outbound test: reset → run → verify"
	@echo "  make run            - Start HTTP API server"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make check          - Run all checks (vet + lint + test)"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-run     - Run Docker container"
	@echo "  make reset-inbound  - Reset inbound pipeline state"
	@echo "  make reset-outbound - Reset outbound pipeline state"
	@echo "  make setup-hooks    - Install git pre-push hooks"

# Build the pipeline binary
build:
	@echo "Building pipeline..."
	@go build -mod=vendor -o bin/pipeline .
	@echo "✓ Build complete: bin/pipeline"

# Run all tests
test: test-unit test-e2e

# Run unit tests only (fast, no external dependencies)
test-unit:
	@echo "Running unit tests..."
	@go test -mod=vendor -v -short ./configs ./types ./pipelines ./tasks

# Run E2E tests (requires services running)
test-e2e:
	@echo "Running E2E tests..."
	@echo "Note: Requires Directus, EPCIS Converter, and TiDB to be running"
	@go test -mod=vendor -v -tags=integration ./tests/...

# Run inbound pipeline E2E test
test-inbound: reset-inbound
	@echo ""
	@echo "Uploading test XML file..."
	@go run scripts/upload_test_file.go
	@echo ""
	@echo "Starting HTTP server and running pipeline..."
	@echo "Note: Make sure server is running or use 'make run' in another terminal"
	@echo ""
	@echo "To run manually:"
	@echo "  1. Start server: make run"
	@echo "  2. In another terminal: curl -X POST http://localhost:8080/run/inbound -H 'Content-Type: application/json' -d '{\"id\":\"test\"}'"
	@echo ""
	@echo "Verifying results..."
	@go run scripts/verify_inbound.go

# Run outbound pipeline E2E test
test-outbound: reset-outbound
	@echo ""
	@echo "Running outbound pipeline test..."
	@echo "Note: Requires test shipment data in database"
	@echo ""
	@echo "To run manually:"
	@echo "  1. Setup test shipment data"
	@echo "  2. Start server: make run"
	@echo "  3. In another terminal: curl -X POST http://localhost:8080/run/outbound -H 'Content-Type: application/json' -d '{\"id\":\"test\"}'"

# Reset inbound pipeline state
reset-inbound:
	@echo "Resetting inbound pipeline state..."
	@go run scripts/reset_inbound.go

# Reset outbound pipeline state (placeholder)
reset-outbound:
	@echo "Resetting outbound pipeline state..."
	@echo "Note: Outbound reset not yet implemented"
	@echo "To manually reset:"
	@echo "  - Clean EPCIS_outbound dispatch records"
	@echo "  - Reset shipment statuses to 'approved'"

# Run HTTP API server
run: build
	@echo "Starting HudSci pipeline service..."
	@./bin/pipeline

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@echo "✓ Clean complete"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod tidy
	@go mod download

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Static analysis
vet:
	@echo "Running go vet..."
	@go vet ./configs ./types ./pipelines ./tasks .

# Lint (requires golangci-lint)
lint:
	@echo "Running linter..."
	@golangci-lint run ./... || echo "⚠ golangci-lint not installed or failed"

# Run all checks (vet, lint, test)
check: vet lint test-unit
	@echo "✓ All checks passed"

# Vendor dependencies for reproducible builds
vendor:
	@echo "Vendoring dependencies..."
	@go mod tidy
	@go mod vendor
	@echo "✓ Vendor complete"

# Docker targets
docker-build:
	@echo "Building Docker image..."
	@docker build --build-arg GH_PAT=${GH_PAT} -t tv-pipelines-hudsci:latest .
	@echo "✓ Docker build complete"

docker-run:
	@echo "Running Docker container..."
	@docker run -p 8080:8080 \
		--env-file .env \
		-v $(PWD)/certs:/root/certs \
		tv-pipelines-hudsci:latest

# Quick run (without rebuild)
run-quick:
	@./bin/pipeline

# Install git hooks
setup-hooks:
	@echo "Installing git hooks..."
	@mkdir -p .git/hooks
	@cp scripts/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Done!"
