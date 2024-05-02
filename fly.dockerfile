FROM golang:alpine as builder

WORKDIR /app
COPY . /app

RUN apk update && apk add --no-cache git tzdata \
    && git rev-parse --short HEAD > commit \
    && GOOS=linux GOARCH=amd64 go build \
      -ldflags='-w -s -extldflags "-static"' \
      -mod=readonly \
      -a \
      -o garry .

FROM scratch
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /app/garry /garry
COPY --from=builder /app/client /client
COPY --from=builder /app/commit /commit

ENTRYPOINT ["/garry -ld fly-global-services:1992"]