.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin tests)"
	go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# The first run exits 1 because node_exporter 0.15.2 serves the old metric
# names, which the example shows on purpose. The second must stay quiet.
demo:
	go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-0.15.2.prom --target-label cluster examples/node-exporter/nodes.json || test $$? -eq 1
	go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-1.12.1.prom --target-label cluster --rules examples/node-exporter/node-exporter-rules.yaml examples/node-exporter/nodes.json examples/node-exporter/node-rsrc-use.json --fail-on never
	go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-1.12.1.prom --target-label cluster examples/node-exporter/nodes.json
