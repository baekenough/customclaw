.PHONY: build-worker build-app build-supervisor build test lint clean docker-build docker-worker docker-app

build-worker:
	go build -o bin/worker ./cmd/worker

build-app:
	go build -o bin/app ./cmd/app

build-supervisor:
	go build -o bin/supervisor ./cmd/supervisor

build: build-worker build-app build-supervisor

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

docker-build:
	docker build -t customclaw-go:latest .

docker-worker:
	docker run --rm customclaw-go:latest worker

docker-app:
	docker run --rm customclaw-go:latest app
