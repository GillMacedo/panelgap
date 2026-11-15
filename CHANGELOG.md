# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-15

### Added
- Check the PromQL in Grafana dashboards, query variables, recording rules and alerts against a `/metrics` scrape, OpenMetrics, or Prometheus API output, and report `missing-metric` and `missing-label`, with similar names the inputs do have.
- Dashboards from JSON files, directories, the Grafana API response, and Kubernetes ConfigMap YAML. Rules from Prometheus rule files and PrometheusRule YAML.
- A PromQL parser that reports a query only when it can return nothing, so `or` fallbacks stay quiet and `unless` filters that remove nothing are warnings.
- `--target-label` for labels Prometheus adds at scrape time, `--fail-on`, and `--format json`.
- Exit 2, with nothing checked, for input that can't be trusted: empty, garbage, cut off, or the output of a failed command.
- `checking-dashboards-against-metrics` skill and plugin manifest.
