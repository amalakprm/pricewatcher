# Stage 1: build
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o pricewatcher .

# Stage 2: minimal runtime
FROM scratch
COPY --from=builder /build/pricewatcher /pricewatcher
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/pricewatcher"]
