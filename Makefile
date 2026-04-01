.PHONY: lint format formatted

lint:
	go tool golangci-lint run --fix ./...

format:
	go tool golangci-lint fmt ./...

formatted:
	go tool golangci-lint fmt --diff ./... && test -z "$$(go tool golangci-lint fmt --diff ./...)"
