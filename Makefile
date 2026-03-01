BINARY = autoversion

.PHONY: build build-all test clean install uninstall

build:
	go build -o $(BINARY) .

build-all:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o build/darwin-amd64/$(BINARY) .
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o build/darwin-arm64/$(BINARY) .
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o build/linux-amd64/$(BINARY) .
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o build/linux-arm64/$(BINARY) .

test:
	go test -v ./...

clean:
	rm -f $(BINARY)
	rm -rf build/

install: build
	cp $(BINARY) /usr/local/bin/$(BINARY)

uninstall:
	rm -f /usr/local/bin/$(BINARY)
