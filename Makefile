GO ?= go
BINARY ?= aehenamer
BUILD_DIR ?= bin
BUILD_FLAGS ?= -trimpath

.PHONY: all build test clean

all: build

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY) .

test:
	CGO_ENABLED=0 $(GO) test ./...

clean:
	rm -f $(BUILD_DIR)/$(BINARY)
