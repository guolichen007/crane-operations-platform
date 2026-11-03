ARG BAOBEI_DEPS=online
FROM golang:1.27.3-alpine AS source
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
FROM source AS online
RUN go mod download
FROM source AS offline
COPY --from=baobei_go_cache . /go/pkg/mod/cache/download/
RUN GOPROXY=file:///go/pkg/mod/cache/download GOSUMDB=off go mod download
FROM ${BAOBEI_DEPS} AS build
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /baobei .
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 baobei
COPY --from=build /baobei /usr/local/bin/baobei
USER baobei
ENTRYPOINT ["baobei"]
