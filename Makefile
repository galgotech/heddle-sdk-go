# Heddle Go SDK Makefile

BINARY_DIR=./bin
GO=go

.PHONY: build test clean

build:
	@echo "Building Go SDK Plugin..."
	mkdir -p $(BINARY_DIR)
	$(GO) build -o $(BINARY_DIR)/heddle-plugin-std ./cmd

test:
	@echo "Testing Go SDK..."
	$(GO) test ./...

clean:
	@echo "Cleaning Go SDK..."
	@rm -rf $(BINARY_DIR)/heddle-plugin-std
