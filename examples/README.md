# Examples

`node-exporter/` holds real files:

- `nodes.json` and `node-rsrc-use.json`: the "Node Exporter / Nodes" and "Node Exporter / USE Method / Node" dashboards, and `node-exporter-rules.yaml`: the node-exporter PrometheusRule, all from [kube-prometheus](https://github.com/prometheus-operator/kube-prometheus) v0.19.0 (`manifests/`), Apache License 2.0. The dashboards were taken out of `grafana-dashboardDefinitions.yaml` unchanged.
- `node_exporter-1.12.1.prom` and `node_exporter-0.15.2.prom`: `curl -s http://127.0.0.1:PORT/metrics` from the official linux-arm64 release binaries, run with default flags in a Linux container on 2026-11-15. The container's hostname in `node_uname_info` was replaced with `example-node` and its eth0 MAC address with `02:00:00:00:00:01`. Nothing else was changed.
- `prometheus-3.16.0-names.json`: `curl -s http://127.0.0.1:19090/api/v1/label/__name__/values` from Prometheus 3.16.0 scraping that node_exporter 1.12.1 every 2 seconds.

Run them with `make demo`, or see the README for the commands and their output.
