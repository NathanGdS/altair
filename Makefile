run:
	@go run ./main.go 

build:
	@go build -o ./bin/altair ./main.go

run-build:
	@./bin/altair

test:
	@go test -v ./...

test-watch:
	@reflex -r '\.go$$' go test -v ./...