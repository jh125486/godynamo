.PHONY: help init deps-update update-tools update-pkgs test tidy static lint vuln-check modernize fmt vet check integration-test
.DEFAULT_GOAL := help

## help: Show this help message
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## init: Bootstrap pinned dev tool binaries (golangci-lint, govulncheck, modernize, gotestsum)
init: update-tools
	@echo "Development environment initialized ✓"

## deps-update: Update dev tools and Go module dependencies to latest
deps-update: update-tools update-pkgs

## update-tools: Update golangci-lint, govulncheck, modernize, and gotestsum to latest
update-tools:
	@echo "Updating dev tools..."
	@go get -tool -modfile=tools/go.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	@go get -tool -modfile=tools/go.mod golang.org/x/vuln/cmd/govulncheck@latest
	@go get -tool -modfile=tools/go.mod golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest
	@go get -tool -modfile=tools/go.mod gotest.tools/gotestsum@latest
	@go mod tidy -modfile=tools/go.mod
	@echo "Dev tools updated ✓"

## update-pkgs: Update Go module dependencies to latest versions
update-pkgs:
	@echo "Updating Go module dependencies..."
	@go get -u -t ./...
	@go mod tidy
	@echo "Go module dependencies updated ✓"

## test: Run all unit tests with coverage
test:
	@echo "Running tests..."
	@go tool -modfile=tools/go.mod gotestsum --format testname -- -timeout 30s -shuffle=on -race -cover -coverprofile=coverage.out ./...

## integration-test: Run integration tests against a real DynamoDB-compatible backend (requires Docker)
integration-test:
	@echo "Running integration tests..."
	@go tool -modfile=tools/go.mod gotestsum --format testname -- -tags=integration -race -v ./...

## tidy: Tidy Go modules
tidy:
	@echo "Tidying Go modules..."
	@go mod tidy
	@echo "Go modules tidied ✓"

## static: Run all linting tools
static: tidy vet lint modernize vuln-check
	@echo "All linting completed ✓"

## lint: Run golangci-lint with auto-fix enabled
lint:
	@echo "Running $$(go tool -modfile=tools/go.mod golangci-lint version)..."
	@go tool -modfile=tools/go.mod golangci-lint run --fix ./...

## vuln-check: Check for known vulnerabilities in dependencies
vuln-check:
	@echo "Checking for vulnerabilities..."
	@go tool -modfile=tools/go.mod govulncheck ./...

## modernize: Check for outdated Go patterns and suggest improvements
modernize:
	@echo "Running modernize analysis..."
	@go tool -modfile=tools/go.mod modernize -fix -test ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

## check: Run all checks (tidy, format, static analysis, test)
check: tidy fmt static test
	@echo "All checks completed ✓"
