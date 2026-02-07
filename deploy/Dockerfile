# SRE Collector Dockerfile (push-first)
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /out/sre-collector ./backend/cmd/collector

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S sre-collector && adduser -S -G sre-collector -h /var/lib/sre-collector -s /sbin/nologin -D sre-collector
RUN mkdir -p /etc/sre-collector /var/lib/sre-collector /var/log/sre-collector \
    && chown -R sre-collector:sre-collector /var/lib/sre-collector /var/log/sre-collector

COPY --from=builder /out/sre-collector /usr/local/bin/sre-collector

EXPOSE 9090

USER sre-collector
ENTRYPOINT ["/usr/local/bin/sre-collector"]
CMD ["--level", "2"]
