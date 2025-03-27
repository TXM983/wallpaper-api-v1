.PHONY: build run test

build:
	docker build -t wallpaper-api .

run:
	docker run -p 8080:8080 -v ${PWD}/configs:/etc/wallpaper-api-v1 wallpaper-api

test:
	go test -v ./...
