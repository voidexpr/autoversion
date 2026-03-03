# Stage 1: build and test
FROM golang:1.24-alpine AS builder

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
RUN mkdir -p /input /output
CMD ["/bin/bash"]

# Stage 3: runtime
FROM alpine:latest

RUN apk add --no-cache git

WORKDIR /app
COPY --from=builder /src/autoversion .

RUN mkdir /input /output

ENTRYPOINT ["./autoversion"]
