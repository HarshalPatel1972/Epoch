.PHONY: all build test run clean benchmark docker-build docker-run

# Run all tests and builds
all: build test

# Go Build with optimized binary
build:
	go build -o bin/epoch main.go

# Comprehensive test run
test:
	go test ./... -v -race

# Local dev run
run:
	go run main.go

# Benchmark execution
benchmark:
	go test -bench=. -benchmem -v ./aggregate

# Cleanup binaries and test artifacts
clean:
	rm -rf bin/
	rm -rf *_test
	rm test_output.txt benchmark_output.txt

# Docker automation
docker-build:
	docker build -t epoch-api .

docker-run:
	docker run -p 8080:8080 --rm epoch-api
