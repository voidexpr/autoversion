BINARY = autoversion

.PHONY: build build-all test clean install uninstall docker-build docker-shell docker-test

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
	go test -v ./...

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
		$(if $(INPUT),-v $(abspath $(INPUT)):/input:ro) \
		$(if $(OUTPUT),-v $(abspath $(OUTPUT)):/output:rw) \
		autoversion-dev

docker-test:
ifndef INPUT
	$(error INPUT is required. Usage: make docker-test INPUT=/path/to/repo)
endif
	$(eval OUTPUT ?= /tmp/autoversion_docker_test/$(shell date +%Y%m%d%H%M%S)-$(shell head -c 4 /dev/urandom | xxd -p))
	@mkdir -p $(OUTPUT)
	@echo "Input:  $(INPUT)"
	@echo "Output: $(OUTPUT)"
	docker run --rm -v $(abspath $(INPUT)):/input:ro -v $(abspath $(OUTPUT)):/output:rw autoversion watch /input --base /output --buffer-time 1
