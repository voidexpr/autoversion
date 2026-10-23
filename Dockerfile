# Stage 1: build and test
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o autoversion .
RUN go test -v ./...

# Stage 2: dev shell (Go toolchain + source + bash)
FROM builder AS dev

RUN apk add --no-cache bash
RUN mkdir -p /source /destination
CMD ["/bin/bash"]

# Stage 3: runtime
FROM alpine:latest

RUN apk add --no-cache git

# Commits are made as root, in destinations mounted from the host (owned by
# another user, which git refuses unless the directory is marked safe)
RUN git config --global user.name autoversion \
 && git config --global user.email autoversion@localhost \
 && git config --global --add safe.directory '*'

WORKDIR /app
COPY --from=builder /src/autoversion .

RUN mkdir /source /destination

ENTRYPOINT ["./autoversion"]
