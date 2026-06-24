APP := podscribe
CMD := ./cmd/$(APP)
BIN := bin/$(APP)
ARGS ?=

.PHONY: help build install run test vet fmt tidy check clean

help:
	@echo "Targets:"
	@echo "  make build    Build $(APP) into ./bin"
	@echo "  make install  Install $(APP) with go install"
	@echo "  make run      Run locally, pass ARGS='doctor'"
	@echo "  make test     Run tests"
	@echo "  make vet      Run go vet"
	@echo "  make fmt      Format Go files"
	@echo "  make tidy     Tidy go.mod/go.sum"
	@echo "  make check    Run fmt, vet, and test"
	@echo "  make clean    Remove build output"

build:
	@mkdir -p bin
	go build -o $(BIN) $(CMD)

install:
	go install $(CMD)

run:
	go run $(CMD) $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

tidy:
	go mod tidy

check: fmt vet test

clean:
	rm -rf bin
