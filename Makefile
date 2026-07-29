.PHONY: help init deps-update test tidy static lint lint-update vuln-check modernize outdated fmt vet check integration-test
.DEFAULT_GOAL := help

## help: Show this help message
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## init: Initialize development environment (goimports)
init:
	@echo "Initializing development environment..."
	@go install golang.org/x/tools/cmd/goimports@latest
	@echo "Development environment initialized ✓"

## deps-update: Update golangci-lint and Go module dependencies
deps-update: lint-update
	@echo "Updating Go modules to latest versions..."
	@go get -u -t ./...
	@go mod tidy
	@echo "Go modules updated ✓"

## test: Run all unit tests with coverage
test:
	@echo "Running tests..."
	@go test -timeout 30s -shuffle=on -race -cover -coverprofile=coverage.out ./...

## integration-test: Run integration tests against a real DynamoDB-compatible backend (requires Docker)
integration-test:
	@echo "Running integration tests..."
	@go test -tags=integration -race -v ./...

## tidy: Tidy Go modules
tidy:
	@echo "Tidying Go modules..."
	@go mod tidy
	@echo "Go modules tidied ✓"

## static: Run all linting tools
static: tidy vet lint modernize vuln-check outdated
	@echo "All linting completed ✓"

## lint: Run golangci-lint with auto-fix enabled
lint:
	@echo "Running $$(go tool -modfile=golangci-lint.mod golangci-lint version)..."
	@go tool -modfile=golangci-lint.mod golangci-lint run --fix ./...

## lint-update: Update golangci-lint to latest version
lint-update:
	@echo "Updating golangci-lint..."
	@go get -tool -modfile=golangci-lint.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@go mod tidy -modfile=golangci-lint.mod
	@echo "Updated $$(go tool -modfile=golangci-lint.mod golangci-lint version)"

vuln-check:
	@echo "Checking for vulnerabilities..."
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## modernize: Check for outdated Go patterns and suggest improvements
modernize:
	@echo "Running modernize analysis..."
	@go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix -test ./...

outdated:
	@echo "Checking for outdated direct dependencies..."
	@go list -u -m -f '{{if not .Indirect}}{{.}}{{end}}' all 2>/dev/null | grep '\[' || echo "All direct dependencies are up to date"

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@go run golang.org/x/tools/cmd/goimports@latest -w .

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

## check: Run all checks (tidy, format, static analysis, test)
check: tidy fmt static test
	@echo "All checks completed ✓"
