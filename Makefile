# claudit — common developer targets.
#
# Default is `install`, not `build`, so plain `make` writes to
# $(GOPATH)/bin/claudit (= the one on PATH). `go build` puts a binary
# at ./claudit which is NOT on PATH — that mismatch was a recurring
# foot-gun where local changes appeared not to take effect because
# `claudit` resolved to a stale go-install copy in ~/go/bin/.

.PHONY: install build test test-go test-js clean

install:
	go install ./cmd/claudit

build:
	go build -o ./claudit ./cmd/claudit

test: test-go test-js

test-go:
	go test ./...

test-js:
	npm test --silent

clean:
	rm -f ./claudit ./claudit.exe
