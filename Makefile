.PHONY: build clean test run build-aarch64 build-all

# Binary name
BINARY_NAME=go-pia-port-forwarding

# Build directory
BUILD_DIR=./bin

# Main package path
MAIN_PACKAGE=./cmd/go-pia-port-forwarding

# Build the application for the current platform
build:
	@echo "Building $(BINARY_NAME) for current platform..."
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Build the application for ARM64/aarch64
build-aarch64:
	@echo "Building $(BINARY_NAME) for ARM64/aarch64..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-arm64 $(MAIN_PACKAGE)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-arm64"

# Build for all supported platforms
build-all: build build-aarch64
	@echo "All builds complete"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...
	@echo "Tests complete"

# Run the application (for development)
run:
	@echo "Running $(BINARY_NAME)..."
	@go run $(MAIN_PACKAGE) $(ARGS)

# Install the application
install:
	@echo "Installing $(BINARY_NAME)..."
	@go install $(MAIN_PACKAGE)
	@echo "Installation complete"
