.PHONY: build run test

build:
	docker build -t wallpaper-api-v1 .

run:
	docker run -p 8080:8080 -v ${PWD}/configs:/etc/wallpaper-api-v1 wallpaper-api-v1

test:
	go test -v ./...
