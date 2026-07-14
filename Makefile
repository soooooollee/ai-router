.PHONY: build test check release-check web web-e2e clean

build: web
	go build -trimpath -ldflags "-s -w" -o bin/air ./cmd/airoute

test:
	go test ./...

check: web
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race ./...

release-check: check
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-linux-amd64 ./cmd/airoute
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-linux-arm64 ./cmd/airoute
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-darwin-amd64 ./cmd/airoute
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-darwin-arm64 ./cmd/airoute
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-windows-amd64.exe ./cmd/airoute
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o /tmp/air-windows-arm64.exe ./cmd/airoute

web:
	cd web && npm run test && npm run build

web-e2e: web
	cd web && npm run test:e2e

clean:
	rm -rf bin web/dist
