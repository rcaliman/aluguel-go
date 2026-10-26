.PHONY: build build-rpi test integration check run

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o bin/aluguel ./cmd/aluguel

build-rpi:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags='-s -w' -o bin/aluguel-linux-arm64 ./cmd/aluguel
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -buildvcs=false -trimpath -ldflags='-s -w' -o bin/aluguel-linux-armv7 ./cmd/aluguel

test:
	go test -race ./...

integration:
	./scripts/test-mysql.sh

check: build
	./bin/aluguel -config config.json -check

run:
	go run ./cmd/aluguel -config config.json
