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

perf-load:
	@bash ./perf/load.sh

perf-validate:
	@bash ./perf/validate.sh smoke

perf-validate-heavy:
	@bash ./perf/validate.sh heavy

perf-cleanup:
	@bash ./perf/cleanup.sh
