FUZZTIME ?= 30s

.PHONY: generate
generate:
	buf generate

# CI runs these as separate jobs, together with a 32-bit run.
.PHONY: test
test:
	go test -count=1 ./...
	go test -count=1 -race ./...

.PHONY: fuzz
fuzz:
	@for target in $$(go test -list '^Fuzz' ./internal/paritytest | grep '^Fuzz'); do \
		echo "fuzz $${target}"; \
		go test -run='^$$' -fuzz="^$${target}$$" -fuzztime=$(FUZZTIME) -fuzzminimizetime=10s ./internal/paritytest || exit 1; \
	done

# Coverage of the protojsonx package from all tests, including the parity
# tests in internal/paritytest.
.PHONY: cover
cover:
	go test -count=1 -coverpkg=. ./...

# The numbers in docs/performance.md come from these two targets.
.PHONY: bench
bench:
	go test -run='^$$' -bench='^Benchmark(Marshal|Unmarshal)$$' -benchmem -count=10 -cpu=1 .
	go test -run='^$$' -bench='^BenchmarkShapes$$/[^/]+/^(Marshal|Unmarshal)$$' -benchmem -count=6 -cpu=1 .

# The parallel variants of BenchmarkShapes, with all CPUs.
.PHONY: bench-parallel
bench-parallel:
	go test -run='^$$' -bench='^BenchmarkShapes$$/[^/]+/Parallel' -benchmem -count=6 .

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: conformance
conformance:
	scripts/conformance.sh protojsonx

.PHONY: conformance-baseline
conformance-baseline:
	scripts/conformance.sh protojson

.PHONY: vulncheck
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

.PHONY: pinact
pinact:
	GITHUB_TOKEN=$$(gh auth token) pinact run --update --verify
