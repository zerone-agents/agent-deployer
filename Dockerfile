# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# Build the static binary
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/server /usr/local/bin/agent-deployer

ENTRYPOINT ["agent-deployer"]
