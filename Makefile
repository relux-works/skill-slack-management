BINARY_NAME := slack-mgmt

.PHONY: build test fmt vet install

build:
	go build -o $(BINARY_NAME) ./cmd/slack-mgmt

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install:
	go build -o $$HOME/.local/bin/$(BINARY_NAME) ./cmd/slack-mgmt
