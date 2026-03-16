BINARY := scion-libp2p
MODULE := github.com/erena/scion-libp2p
RESULTS := results
TIMESERIES := $(RESULTS)/timeseries
RUNS := 10
REQUESTS := 100

.PHONY: build test test-integration vet lint clean proto bench bench-ablation bench-fault reproduce monitor

build:
	go build -o $(BINARY) .

test:
	go test ./...

test-integration:
	go test -tags=integration -timeout 120s ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/scionlibp2p.proto

# --- Benchmarks ---

bench:
	go run . bench --experiment compare --nodes 5 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/compare_n5.csv --output-timeseries $(TIMESERIES)

bench-scalability:
	go run . bench --experiment scalability --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/scalability.csv --output-timeseries $(TIMESERIES)

bench-ablation:
	go run . bench --experiment ablation --nodes 10 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/ablation.csv

bench-fault:
	go run . bench --experiment fault --nodes 10 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/fault_injection.csv

# --- Full Reproducibility ---

reproduce: build
	@echo "=== Full Reproducibility Run ==="
	@echo "Runs per config: $(RUNS), Requests per run: $(REQUESTS)"
	@echo ""
	@mkdir -p $(RESULTS) $(TIMESERIES)
	@echo "--- Step 1/5: Seven-way comparison (N=5) ---"
	go run . bench --experiment compare --nodes 5 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/compare_n5.csv --output-timeseries $(TIMESERIES)
	@echo "--- Step 2/5: Seven-way comparison (N=10) ---"
	go run . bench --experiment compare --nodes 10 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/compare_n10.csv --output-timeseries $(TIMESERIES)
	@echo "--- Step 3/5: Seven-way comparison (N=25) ---"
	go run . bench --experiment compare --nodes 25 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/compare_n25.csv --output-timeseries $(TIMESERIES)
	@echo "--- Step 4/5: Ablation study (N=10) ---"
	go run . bench --experiment ablation --nodes 10 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/ablation.csv
	@echo "--- Step 5/5: Fault injection (N=10) ---"
	go run . bench --experiment fault --nodes 10 --requests $(REQUESTS) --runs $(RUNS) \
		--output-csv $(RESULTS)/fault_injection.csv
	@echo ""
	@echo "--- Generating plots ---"
	python $(RESULTS)/plot_convergence.py || echo "Warning: plot generation failed (matplotlib required)"
	@echo ""
	@echo "=== Reproducibility run complete ==="
	@echo "Results in $(RESULTS)/"
	@echo "  compare_n{5,10,25}.csv    - Policy comparisons with 95% CI"
	@echo "  ablation.csv              - Ablation study"
	@echo "  fault_injection.csv       - Fault injection results"
	@echo "  convergence_n{5,10,25}.png - Convergence plots"
	@echo "  comparison_n{5,10,25}.png  - Comparison bar charts"

monitor:
	docker compose up -d
