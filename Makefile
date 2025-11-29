.PHONY: lint test vendor clean

export GO111MODULE=on

default: lint test

lint:
	golangci-lint run

test:
	go test -v -cover ./...

yaegi_test:
	@command -v yaegi >/dev/null 2>&1 || { echo "yaegi not found; install with 'go install github.com/traefik/yaegi/cmd/yaegi@latest'"; exit 1; }
	@tmpdir=$$(mktemp -d); trap 'rm -rf "$$tmpdir"' EXIT INT TERM; \
	modpath="$$tmpdir/src/github.com/KCL-Electronics/traefik-cdn-whitelist/v2"; \
	mkdir -p "$$modpath"; \
	rsync -a --delete --exclude '.git' ./ "$$modpath"; \
	if [ -d vendor/github.com ]; then \
		mkdir -p "$$tmpdir/src/github.com" && rsync -a vendor/github.com/ "$$tmpdir/src/github.com/"; \
	fi; \
	cd "$$modpath" && GO111MODULE=off GOPATH="$$tmpdir" yaegi test .


vendor:
	go mod vendor

clean:
	rm -rf ./vendor