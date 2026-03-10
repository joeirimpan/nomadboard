BIN := nomadboard
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run clean test dist

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

run: build
	./$(BIN) -config config.huml

test:
	go test ./...

dist:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BIN) .
	cd dist && tar czf $(BIN).tar.gz $(BIN)
	rm -f dist/$(BIN)
	@echo "dist/$(BIN).tar.gz"

clean:
	rm -f $(BIN)
	rm -rf dist/

