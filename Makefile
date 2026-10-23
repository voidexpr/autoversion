BINARY = autoversion

.PHONY: build build-all test test-verbose lint fmt clean install uninstall docker-build docker-shell docker-test

build:
	go build -o $(BINARY) .

UNAME_S := $(shell uname -s)

build-all:
ifeq ($(UNAME_S),Darwin)
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o build/darwin-amd64/$(BINARY) .
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o build/darwin-arm64/$(BINARY) .
endif
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o build/linux-amd64/$(BINARY) .
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o build/linux-arm64/$(BINARY) .

test:
	go test ./...

test-verbose:
	go test -v ./...

lint:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
	rm -rf build/

install: build
	cp $(BINARY) /usr/local/bin/$(BINARY)

uninstall:
	rm -f /usr/local/bin/$(BINARY)

docker-build:
	docker build -t autoversion .

docker-shell:
	docker build --target dev -t autoversion-dev .
	docker run -it --rm \
		$(if $(SOURCE),-v $(abspath $(SOURCE)):/source:ro) \
		$(if $(DESTINATION),-v $(abspath $(DESTINATION)):/destination:rw) \
		autoversion-dev

docker-test: docker-build
	$(eval SOURCE ?= test_watch)
	$(eval DESTINATION ?= /tmp/autoversion_docker_test/$(shell date +%Y%m%d%H%M%S)-$(shell head -c 4 /dev/urandom | xxd -p))
	@mkdir -p $(DESTINATION)
	@echo "Source:      $(SOURCE)"
	@echo "Destination: $(DESTINATION)"
	$(eval DOCKER_RUN := docker run --rm -v $(abspath $(SOURCE)):/source:ro -v $(abspath $(DESTINATION)):/destination:rw autoversion)
	$(DOCKER_RUN) init --source /source --destination /destination
	$(DOCKER_RUN) watch --source /source --destination /destination --buffer-time 1
