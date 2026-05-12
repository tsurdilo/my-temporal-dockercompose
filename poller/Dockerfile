FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o poller .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/poller /poller
ENTRYPOINT ["/poller"]
