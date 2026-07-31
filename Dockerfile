FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/navermap-sync ./cmd/navermap-sync \
 && CGO_ENABLED=0 go build -ldflags='-s -w' -o /out/navermap-mcp ./cmd/navermap-mcp

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/ /usr/local/bin/
# 운영에서는 /data/navermap/config.json 을 /data/config.json 으로 마운트한다
CMD ["navermap-sync", "-config", "/data/config.json"]
