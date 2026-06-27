# ---------- Build ----------
FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /pricewatcher .

# ---------- Runtime ----------
FROM scratch

COPY --from=builder /pricewatcher /pricewatcher
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8420

ENTRYPOINT ["/pricewatcher"]
