.PHONY: test build tidy clean fmt vet migrate tofu-init tofu-validate tofu-plan

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

migrate:
	go run ./cmd/nightloch migrate

clean:
	rm -rf bin/

tofu-init:
	cd infra/opentofu && tofu init

tofu-validate:
	cd infra/opentofu && tofu validate

# Requires terraform.tfvars + DIGITALOCEAN_TOKEN + CLOUDFLARE_API_TOKEN
tofu-plan:
	cd infra/opentofu && tofu plan
