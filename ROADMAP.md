# Roadmap

## Next
- `--ignore-metric` for metrics that are known to appear only later, such as error counters
- Grafana's v2 dashboard schema (`elements`), which is refused today
- Label value checks for fixed values, such as `mode="idle"`, as notes
- A GitHub Actions example that scrapes a service started in CI and checks the repository's dashboards

## Later
- Reading Prometheus scrape configs, so labels added by relabeling don't need `--target-label`
- Loki queries against log labels
- Alertmanager routes that match labels no alert sets
