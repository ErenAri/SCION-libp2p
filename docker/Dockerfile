# Multi-stage build for pathaware-libp2p WAN simulation
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /pathaware-libp2p .

# Runtime image with tc (iproute2) for network emulation
FROM alpine:3.20

RUN apk add --no-cache iproute2 bash curl jq

COPY --from=builder /pathaware-libp2p /usr/local/bin/pathaware-libp2p
COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 9000 9090 2112

ENTRYPOINT ["/entrypoint.sh"]
