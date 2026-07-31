IMAGE = kobums/navermap
TAG ?= latest

.PHONY: build push

build:
	go build ./...

# 맥(arm64)에서 서버(amd64)용 이미지를 빌드해 Docker Hub 로 푸시
push:
	docker buildx build --platform linux/amd64 -t $(IMAGE):$(TAG) --push .
