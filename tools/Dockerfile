FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum

RUN go mod download

COPY . .

ENV GOCACHE=/root/.cache/go-build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /coach ./cmd/coach

FROM alpine

WORKDIR /app

# Install git, docker compose, and other essential binaries
RUN apk add --no-cache \
    git \
    docker \
    docker-compose \
    curl \
    ca-certificates \
    openssh-client \
    bash

COPY --from=builder /coach /coach

EXPOSE 8080

ENTRYPOINT ["/coach"]
