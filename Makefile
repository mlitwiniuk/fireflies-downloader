BINARY := fireflies-downloader
CMD := ./cmd/fireflies-downloader

.PHONY: test build run serve tidy

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/$(BINARY) $(CMD)

run:
	go run $(CMD)

serve:
	go run $(CMD) serve --db fireflies_export/fireflies.sqlite

tidy:
	go mod tidy
