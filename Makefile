BINARY := scion-libp2p
MODULE := github.com/erena/scion-libp2p

.PHONY: build test test-integration vet lint clean proto bench monitor

build:
	go build -o $(BINARY) .

test:
	go test ./...

test-integration:
	go test -tags=integration -timeout 120s ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/scionlibp2p.proto

bench:
	go run . bench --experiment compare --nodes 5 --output-csv results.csv --output-json results.json

monitor:
	docker compose up -d
