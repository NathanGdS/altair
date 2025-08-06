run:
	@go run ./main.go 

build:
	@go build -o altair ./main.go

run-build:
	@./altair

test:
	@go test -v ./...

test-watch:
	@reflex -r '\.go$$' go test -v ./...