.PHONY: all build test fraud-demo train-ml clean

all: build test

build:
	go build -o bin/fraudctl.exe ./cmd/fraudctl
	go build -o bin/fraud-service.exe ./cmd/fraud-service
	go build -o bin/outbox-relay.exe ./cmd/outbox-relay
	go build -o bin/load-gen.exe ./cmd/load-gen

test:
	go test -v ./...

train-ml:
	python ml/train.py

fraud-demo:
	powershell -ExecutionPolicy Bypass -File ./demo.ps1 fraud-demo

clean:
	rm -rf bin/
