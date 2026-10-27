# Everything runs inside `nix develop`. CGO is off: the build is pure Go.
export CGO_ENABLED := 0

LIBRARY ?= ./testdata/golden-bundle
CSS_IN  := internal/web/static/input.css
CSS_OUT := internal/web/static/app.css

.PHONY: all generate css build run test lint check clean

all: check build

generate:
	go tool templ generate

css:
	tailwindcss -i $(CSS_IN) -o $(CSS_OUT) --minify

build: generate css
	go build -o bin/reader ./cmd/reader

run: generate css
	go run ./cmd/reader -library $(LIBRARY)

test: generate
	go test ./...

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

# CI gate: generated templ code must be committed and up to date.
check: lint test
	git diff --exit-code -- '*_templ.go'

clean:
	rm -rf bin $(CSS_OUT)
