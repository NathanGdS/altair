run:
	@go run ./main.go 

build:
	@go build -o altair ./main.go

run-build:
	@./altair