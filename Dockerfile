FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git

# Copy local module sources to the paths matching go.mod replace directives
COPY temporal/temporal /deps/temporal
COPY temporal-etcd-dynconfig /deps/temporal-etcd-dynconfig
COPY my-temporal-dockercompose/server /app

WORKDIR /app
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -o temporal-server .

FROM alpine:latest
RUN apk add --no-cache bash ca-certificates tzdata netcat-openbsd
COPY --from=builder /app/temporal-server /usr/local/bin/temporal-server
COPY my-temporal-dockercompose/server/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["start"]
