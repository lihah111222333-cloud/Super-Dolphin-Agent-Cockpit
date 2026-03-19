.PHONY: test lint build vet

test:
	go test -race ./...

vet:
	go vet ./...

lint: vet
	@echo "lint passed"

build:
	go build ./...

check: lint test
	@echo "all checks passed"
