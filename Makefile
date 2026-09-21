.PHONY: build test vet fmt run compare validate clean

SCENARIO ?= scenarios/base.json
CONTROLLER ?= greedy
SEED ?= 42

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

validate: build
	go run ./cmd/cto validate -scenario $(SCENARIO)

run: build
	go run ./cmd/cto run -scenario $(SCENARIO) -controller $(CONTROLLER) -seed $(SEED) -out summary.json -out-series series.csv

compare: build
	@for s in scenarios/*.json; do \
		echo "== $$s"; \
		go run ./cmd/cto compare -scenario $$s -controllers naive,greedy -seeds 1,2,3,4,5; \
		echo; \
	done

clean:
	rm -f summary.json series.csv
