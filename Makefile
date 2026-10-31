BINARY = autoversion

.PHONY: build build-all test test-verbose filter lint fmt clean install uninstall watch-check docker-build docker-dev-build docker-shell docker-test docker-go-test docker-sh docker-watch-check

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

# Regenerates filters/README.md from the group files (TestFiltersDoc fails
# while it is stale)
filter:
	go test -count=1 -run TestFiltersDoc . -args -update

# Runs a real watch on a temp source, with git, with --no-git and with an
# allowlist, and checks file and directory changes, the rules and no copy
# loop (scripts/check_watch.sh). Built under build/, so the ./autoversion run
# by installed jobs isn't replaced
WATCH_CHECK_BINARY = build/watch-check/$(BINARY)
watch-check:
	go build -o $(WATCH_CHECK_BINARY) .
	scripts/check_watch.sh $(WATCH_CHECK_BINARY)
	scripts/check_watch.sh $(WATCH_CHECK_BINARY) --no-git
	scripts/check_watch.sh $(WATCH_CHECK_BINARY) --allowlist

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

# Go toolchain image (Dockerfile dev stage) used by docker-shell, docker-go-test
# and docker-sh. Rebuild it when go.mod changes (its module cache comes from the build).
docker-dev-build:
	docker build --target dev -t autoversion-dev .

docker-shell: docker-dev-build
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

# docker-go-test and docker-sh only run containers from the autoversion-dev
# image (no image build), with the current source mounted at /src, so code
# changes don't need a rebuild
DOCKER_DEV_RUN = docker run --rm -v $(CURDIR):/src -w /src \
	-v autoversion-go-cache:/root/.cache/go-build \
	-e GOFLAGS=-buildvcs=false \
	-e GIT_AUTHOR_NAME=autoversion -e GIT_AUTHOR_EMAIL=autoversion@localhost \
	-e GIT_COMMITTER_NAME=autoversion -e GIT_COMMITTER_EMAIL=autoversion@localhost \
	autoversion-dev

# Go tests on Linux, e.g. make docker-go-test GOTEST_ARGS='-v -run TestShouldTrack'
docker-go-test:
	$(DOCKER_DEV_RUN) go test -count=1 $(GOTEST_ARGS) ./...

# Shell command on Linux, e.g.
# make docker-sh CMD='go build -o /tmp/av . && /tmp/av watch --report-only --source /tmp'
# CMD is passed as typed ($(value)), so $VAR and quotes reach the container shell
override CMD := $(value CMD)
export CMD
docker-sh:
	$(DOCKER_DEV_RUN) sh -c "$$CMD"

# watch-check on Linux (inotify)
docker-watch-check:
	$(DOCKER_DEV_RUN) sh -c 'go build -o /tmp/autoversion . && scripts/check_watch.sh /tmp/autoversion && scripts/check_watch.sh /tmp/autoversion --no-git && scripts/check_watch.sh /tmp/autoversion --allowlist'
