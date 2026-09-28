# SETU regulatory intelligence engine.
#
# Every target is reproducible: the anchor date is fixed rather than taken from
# the clock, so a run today and a run next year give the same answer.

GO      ?= go
CONTENT ?= content/jurisdictions/IN-TN
ASOF    ?= 2026-09-04
SEED    ?= 20260101
CASES   ?= 2000

.PHONY: all check build vet test content golden gates analyze serve fmt clean help

all: gates

help:
	@echo "make check    - build, vet and unit tests"
	@echo "make content  - check, test and analyse the seeded ruleset"
	@echo "make golden   - the release-gating regression suite"
	@echo "make gates    - everything CI runs"
	@echo "make serve    - run the console at http://localhost:8080"

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

check: build vet test

content:
	$(GO) run ./cmd/setuctl check   --as-of $(ASOF) $(CONTENT)
	$(GO) run ./cmd/setuctl test    --as-of $(ASOF) --property-cases $(CASES) --seed $(SEED) $(CONTENT)
	$(GO) run ./cmd/setuctl analyze --as-of $(ASOF) $(CONTENT)

# The browser console, served by the engine itself. Single binary, no build step.
serve:
	$(GO) run ./cmd/rulesd --content $(CONTENT)

# The console pinned to a fixed date, so a demo gives the same answer every time.
serve-pinned:
	$(GO) run ./cmd/rulesd --content $(CONTENT) --as-of $(ASOF)

analyze:
	$(GO) run ./cmd/setuctl analyze --as-of $(ASOF) $(CONTENT)

golden:
	$(GO) test ./test/golden

# The full gate. A release candidate ships only when this passes.
gates: check content golden
	@echo
	@echo "all gates passed"

clean:
	rm -rf dist
