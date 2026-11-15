---
name: checking-dashboards-against-metrics
description: Checks the PromQL in Grafana dashboards and Prometheus or PrometheusRule rule files against the metrics an exporter or Prometheus really has, and reports queries that select metric names or labels that aren't there, so panels show No data, template variables are empty, or alerts can't fire. Suggests the closest real names after a rename such as node_exporter 0.16. Use when a Grafana panel shows No data, after upgrading an exporter or renaming a metric in code, before merging dashboard or alert changes, or when asked whether alerts would ever fire.
---

# Checking dashboards against metrics

panelgap reads files only. It needs Go 1.22 or newer to build, and never connects to Grafana, Prometheus or the exporter. Build it once:

    go build -o "${TMPDIR:-/tmp}/panelgap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)

## Get the metrics

Ask before running anything that touches a cluster or a remote host. Any one of these works, and `--metrics` can be repeated for several targets:

- A scrape of the exporter or service: `curl -s http://localhost:9100/metrics > scrape.prom`, or through `kubectl port-forward`.
- The names Prometheus stores: `curl -s http://prometheus:9090/api/v1/label/__name__/values > names.json`. It has no labels, so label checks are skipped.
- Series with labels: `curl -s -g 'http://prometheus:9090/api/v1/series?match[]={job="node"}' > series.json`.

A scrape of a service that just started can miss counters and histograms that nobody has used yet, because client libraries don't serve a labeled metric until it has a value. Scrape after some traffic, or use the Prometheus API.

## Run

    "${TMPDIR:-/tmp}/panelgap" --metrics scrape.prom --rules rules.yaml dashboards/

Dashboards can be JSON files, directories of them, or Kubernetes YAML with dashboard ConfigMaps. `--rules` takes Prometheus rule files or PrometheusRule YAML; recording rules count as metrics, and every rule's expr is checked. Pass `--target-label NAME` for labels Prometheus adds at scrape time through relabeling or external labels, such as `cluster`, `namespace` or `pod`. `job` and `instance` are always treated that way.

Exit 0 is clean, 1 means findings at or above `--fail-on` (default `error`), and 2 means an input couldn't be trusted and nothing was checked. On exit 2, show the user the reason and fix the input. Never report exit 2 as clean.

## Read the findings

- `missing-metric` ERROR: the metric isn't in any input or recording rule. Check `similar names` first: after an exporter upgrade it's often a rename, such as `node_cpu` to `node_cpu_seconds_total`. A name with colons is usually a recording rule, so ask for the rule file. Otherwise the collector may be disabled, the hardware absent (RAID, bonding), or the target not scraped.
- `missing-label` WARN: no series of the metric has the label the matcher needs. If Prometheus adds it with relabeling, rerun with `--target-label`. Otherwise the exporter may have renamed the label.
- `missing-metric` WARN on the right of `unless`: the filter removes nothing.
- `not-checked` WARN: a dashboard query panelgap couldn't parse, so it wasn't checked. Read it yourself; if it's a Loki or other non-Prometheus query, the target's datasource type is missing.
- `not-checked` note: a library panel, whose queries live in Grafana.

A query with `or` is only reported when no branch that selects a metric returns data, so `new_name or old_name` fallbacks stay quiet. A fallback to a constant, such as `or vector(0)`, doesn't hide a missing metric.

## Report

1. Group findings by cause: renamed metrics, missing recording rules, collectors or targets not scraped, labels added by relabeling.
2. For each, give the smallest fix: the new name in the query, the rule file to deploy, the collector flag, or `--target-label`.
3. Say which metrics input was used and how many names and series it had. A scrape of one host doesn't show metrics that only other hosts serve.

## Don't

Don't edit dashboards or rules unless the user asks. Don't call a dashboard correct because panelgap is quiet: it checks that names and labels exist, not that queries are right.
