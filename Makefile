VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
OUT     := plugins/cc-router/libexec
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := darwin-amd64 darwin-arm64 linux-amd64 linux-arm64

.PHONY: test build dist validate clean

test:
	go vet ./...
	go test ./...

# Build for this machine only (fast dev loop).
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/cc-router-$$(go env GOOS)-$$(go env GOARCH) ./cmd/cc-router

# Build every supported platform. Run before tagging a release; the binaries
# are committed so a plain `claude plugin install` works with no toolchain.
dist:
	@for t in $(TARGETS); do \
		os=$${t%-*}; arch=$${t#*-}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/cc-router-$$os-$$arch ./cmd/cc-router || exit 1; \
	done

validate:
	claude plugin validate --strict .
	claude plugin validate --strict plugins/cc-router

clean:
	rm -f $(OUT)/cc-router-*
