# Test data

- `kube-prometheus/`: `manifests/grafana-dashboardDefinitions.yaml` and the `*-prometheusRule.yaml` files from [kube-prometheus](https://github.com/prometheus-operator/kube-prometheus) v0.19.0, unchanged, under the Apache License 2.0 in `kube-prometheus/LICENSE`.
- `prometheus/prometheus-3.16.0-series.json`: `/api/v1/series?match[]={job="node-exporter"}` from Prometheus 3.16.0 scraping node_exporter 1.12.1 in a Linux container.
- `bad/`: real output of failed commands, captured on 2026-11-15: `go build` of a package with an undefined name, `go test` of a failing test, node_exporter's landing page (`/` instead of `/metrics`) and its 404 page, a Prometheus API error for a bad `match[]`, and an API result with no names.
