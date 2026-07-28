.PHONY: test build tidy clean fmt vet

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

build:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.version=dev" -o bin/nightloch-linux-amd64 ./cmd/nightloch
	CGO_ENABLED=0 go build -ldflags "-X main.version=dev" -o bin/nightloch ./cmd/nightloch

tidy:
	go mod tidy

clean:
	rm -rf bin/
