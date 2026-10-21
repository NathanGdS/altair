EXAMPLE ?= 01-consumer
# perf/load.* read the consumer example from the environment.
export EXAMPLE

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

# Perf targets dispatch per OS. On Windows make runs recipes through cmd.exe, where `VAR=1 cmd`
# is not valid and `bash` may resolve to WSL's System32\bash.exe instead of Git Bash, so the
# PowerShell scripts are used there; everywhere else the bash scripts are.
ifeq ($(OS),Windows_NT)
PERF_PS = powershell -NoProfile -ExecutionPolicy Bypass -File

perf-load:
	@$(PERF_PS) perf/load.ps1

perf-load-interactive:
	@$(PERF_PS) perf/load.ps1 -Interactive

perf-validate:
	@$(PERF_PS) perf/validate.ps1 -Mode smoke -Example $(EXAMPLE)

perf-validate-heavy:
	@$(PERF_PS) perf/validate.ps1 -Mode heavy -Example $(EXAMPLE)

perf-validate-interactive:
	@$(PERF_PS) perf/validate.ps1 -Mode heavy -Example $(EXAMPLE) -Interactive

perf-cleanup:
	@$(PERF_PS) perf/cleanup.ps1
else
perf-load:
	@bash ./perf/load.sh

perf-load-interactive:
	@INTERACTIVE=1 bash ./perf/load.sh

perf-validate:
	@bash ./perf/validate.sh smoke $(EXAMPLE)

perf-validate-heavy:
	@bash ./perf/validate.sh heavy $(EXAMPLE)

perf-validate-interactive:
	@INTERACTIVE=1 bash ./perf/validate.sh heavy $(EXAMPLE)

perf-cleanup:
	@bash ./perf/cleanup.sh
endif
