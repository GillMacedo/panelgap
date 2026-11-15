# panelgap: find Grafana panels that will show No data and Prometheus alerts that can't fire

[![CI](https://github.com/GillMacedo/panelgap/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/panelgap/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for people who keep Grafana dashboards and Prometheus alerting rules in a repository. It checks the PromQL in dashboards, template variables, recording rules and alerts against a real `/metrics` scrape or the Prometheus API, and reports every query that selects a metric name or label the exporter doesn't serve, with the closest names it does serve.**

Dashboards and alerts break quietly. An exporter upgrade renames `node_cpu` to `node_cpu_seconds_total`, someone renames a metric in the service's code, a collector is off, or a recording rule was never deployed. Grafana shows "No data", an alert's expression matches nothing and so never fires, and nothing fails in CI. Existing tools such as cardamon and `mimirtool analyze` look the other way: they find metrics that nothing uses, and they need a live Prometheus or Grafana. panelgap needs only files, so it runs in CI next to the dashboards.

## What you can ask Claude

- "This Grafana panel shows No data. Is the metric name wrong?"
- "We upgraded node_exporter. Which dashboards and alerts still use the old metric names?"
- "Will these alert rules ever fire against what our service exposes?"
- "I renamed a metric in the Go code. What dashboards and alerts break?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/panelgap
    claude plugin install panelgap@panelgap-marketplace

Or run it with Go 1.22 or newer. It has no dependencies outside this repository:

    curl -s http://localhost:9100/metrics > scrape.prom
    go run github.com/GillMacedo/panelgap/plugin/scripts@v0.1.0 --metrics scrape.prom --rules rules.yaml dashboards/

## Example

`examples/node-exporter` has the "Node Exporter / Nodes" and "USE Method / Node" dashboards and the node-exporter rules from kube-prometheus v0.19.0, and two real scrapes taken in a Linux container: node_exporter 1.12.1, and node_exporter 0.15.2, from before the 0.16 release renamed most metrics. The dashboards are written for 1.x. Against 0.15.2, every panel is reported, with the name the old version used. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-0.15.2.prom --target-label cluster --fail-on never examples/node-exporter/nodes.json | sed -n '1,11p'
    ERROR missing-metric: Node Exporter / Nodes, panel "CPU Usage" (id 2), query 1
      node_cpu_seconds_total is not in any metrics input or recording rule. The query selects nothing there, so the panel shows No data, or misses those series
      similar names in the metrics inputs: node_cpu
      fix: if it's the same metric under another exporter version's name, query node_cpu instead, or run the version the query expects
      in examples/node-exporter/nodes.json

    ERROR missing-metric: Node Exporter / Nodes, panel "Load Average" (id 3), query 4
      node_cpu_seconds_total is not in any metrics input or recording rule. The query selects nothing there, so the panel shows No data, or misses those series
      similar names in the metrics inputs: node_cpu
      fix: if it's the same metric under another exporter version's name, query node_cpu instead, or run the version the query expects
      in examples/node-exporter/nodes.json

    $ go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-0.15.2.prom --target-label cluster --fail-on never examples/node-exporter/nodes.json | tail -n 2
    18 errors, 0 warnings and 0 notes in 19 queries (24 selectors) from 1 dashboard, against 654 metric names from 1 metrics input.
    Metrics input examples/node-exporter/node_exporter-0.15.2.prom: text exposition, 654 names in 1003 series.

The same dashboard against the node_exporter it was written for stays quiet and exits 0:

    $ go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-1.12.1.prom --target-label cluster examples/node-exporter/nodes.json
    No problems in 19 queries (24 selectors) from 1 dashboard, against 313 metric names from 1 metrics input.
    Metrics input examples/node-exporter/node_exporter-1.12.1.prom: text exposition, 313 names in 1411 series.

`--target-label cluster` says that Prometheus adds a `cluster` label when it scrapes, so it can't be in a scrape. Without it, the dashboard's hidden `$cluster` variable gets a note, because it would have no values. `job` and `instance` are always treated this way.

The USE Method dashboard reads recording rules such as `instance:node_cpu_utilisation:rate5m`. Without the rule file, all 10 of its queries are reported, with a hint:

    $ go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-1.12.1.prom --target-label cluster --fail-on never examples/node-exporter/node-rsrc-use.json | sed -n '1,4p'
    ERROR missing-metric: Node Exporter / USE Method / Node, panel "CPU Utilisation" (id 2), query 1
      instance:node_cpu_utilisation:rate5m is not in any metrics input or recording rule. The query selects nothing there, so the panel shows No data, or misses those series
      fix: the colons suggest a recording rule; pass the rule file that records it with --rules
      in examples/node-exporter/node-rsrc-use.json

With `--rules`, the recording rules count as metrics and both dashboards are quiet. The rules' own expressions are checked too, and 5 of the 41 rules are alerts that can't fire on this host: the RAID, bonding and systemd alerts select metrics node_exporter only serves when that hardware or the systemd collector is there:

    $ go run ./plugin/scripts --metrics examples/node-exporter/node_exporter-1.12.1.prom --target-label cluster --rules examples/node-exporter/node-exporter-rules.yaml --fail-on never examples/node-exporter/nodes.json examples/node-exporter/node-rsrc-use.json | grep -E '^ERROR|^  node_|^[0-9]+ errors'
    ERROR missing-metric: group node-exporter, alert NodeRAIDDegraded
      node_md_disks_required is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeRAIDDegraded
      node_md_disks is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeRAIDDiskFailure
      node_md_disks is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeSystemdServiceFailed
      node_systemd_unit_state is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeSystemdServiceCrashlooping
      node_systemd_service_restart_total is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeBondingDegraded
      node_bonding_slaves is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    ERROR missing-metric: group node-exporter, alert NodeBondingDegraded
      node_bonding_active is not in any metrics input or recording rule. The alert's expression selects nothing there, so it can't fire from that part
    7 errors, 0 warnings and 0 notes in 73 queries (118 selectors) from 2 dashboards and 41 rules, against 313 metric names from 1 metrics input.

Instead of a scrape, you can give the names a real Prometheus stores. This file came from Prometheus 3.16.0 scraping the same node_exporter 1.12.1, with `curl -s http://localhost:9090/api/v1/label/__name__/values`:

    $ go run ./plugin/scripts --metrics examples/node-exporter/prometheus-3.16.0-names.json --target-label cluster examples/node-exporter/nodes.json
    No problems in 19 queries (24 selectors) from 1 dashboard, against 318 metric names from 1 metrics input.
    Metrics input examples/node-exporter/prometheus-3.16.0-names.json: label values API, 318 names.

Add `--format json` for machine-readable output.

## What it reads

- **Metrics:** Prometheus text exposition (a `/metrics` scrape), OpenMetrics, or JSON from the Prometheus API: `/api/v1/label/__name__/values` (names only, so labels aren't checked) or `/api/v1/series` (names and labels). Repeat `--metrics` for several targets; `-` reads stdin. `up`, `scrape_*` and `ALERTS` count as present, because Prometheus writes them itself.
- **Dashboards:** Grafana dashboard JSON (schema with `panels`, nested row panels, or old `rows`), the Grafana API's `{"dashboard": ...}` response, directories of `.json` files, and Kubernetes YAML with ConfigMaps whose `.json` keys hold dashboards, as kube-prometheus and the Grafana sidecar use. It checks each panel target's `expr` and each query variable (`label_values(metric, label)`, `query_result(...)` or plain PromQL). Targets on Loki and other non-Prometheus datasources are skipped, using the datasource type or the dashboard's datasource variables.
- **Rules:** Prometheus rule files (`groups:`) and PrometheusRule objects. Recording rules add their names; every rule's `expr` is checked.

Grafana variables such as `$instance` or `${__rate_interval}` are handled: a matcher whose value uses a variable isn't judged, and a metric name built from a variable, such as `node_${kind}_bytes`, counts as present when any name matches the pattern.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `missing-metric` | ERROR | A selector names a metric that's in no metrics input or recording rule, and the query returns nothing because of it. Up to three similar names are suggested |
| `missing-label` | WARN | A matcher such as `mode="idle"` needs a label that no series of the metric has. Prometheus may add it when scraping; say so with `--target-label` |
| `missing-metric` | WARN | The missing metric is on the right of `unless`, so the filter removes nothing |
| `missing-label` | note | A variable reads a label no series has, but its All value (such as `.*`) still matches, so panels keep working |
| `not-checked` | WARN | A dashboard query panelgap couldn't parse, so it wasn't checked. (In a rule file, such a query makes panelgap refuse the file.) |
| `not-checked` | note | A library panel, whose queries live in Grafana |

A query with `or` is reported only when no branch that selects a metric returns data, so `node_cpu_seconds_total or node_cpu` fallbacks stay quiet, while a branch that mixes old and new names is caught. A constant fallback, such as `or vector(0)`, doesn't hide a missing metric, because the panel would then show that constant forever. Metrics inside `absent()` aren't reported, because a missing metric is what `absent()` is for.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `error` by default. Use `--fail-on warn` to fail on warnings too, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | An input can't be trusted or the flags are wrong. Nothing was checked, and stdout is empty. |

## When it refuses

panelgap exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, when the scrape is node_exporter's HTML landing page, because the `/metrics` path was left off the URL:

    go run ./plugin/scripts --metrics tests/testdata/bad/ne-landing.html examples/node-exporter/nodes.json

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    panelgap: can't trust the input, nothing was checked: tests/testdata/bad/ne-landing.html: line 1 is not a metrics sample ("<html" is not a metric name): <html lang="en">; is it the output of a failed command?

It does the same, on any of its three inputs, for an empty file, random text, binary data, the output of a failed `go build`, `go test`, `curl`, `kubectl` or `helm`, an HTTP error page, a Prometheus API error or empty result, JSON of another kind, a missing file, and a file cut off partway. A scrape is also refused when its last line has no line break, when it ends on a `# HELP` or `# TYPE` line, or when its last histogram or summary stops before `_sum` or `_count`. A dashboard must be valid JSON with `panels` or `rows`; Grafana's newer v2 schema isn't read yet and is refused. A rule file is refused when a group has no rules, a rule has no `expr`, both or neither of `record` and `alert`, an `expr` that doesn't parse (Prometheus would refuse the file too), or `{{ }}` left from a template. One bad file among good ones fails the whole run, and a run where no query selects a metric by name is refused as having nothing to check.

A scrape or rule file cut exactly on a line break can still be valid, and no tool can tell it from a shorter file. Losing scrape lines can only add findings, never hide one. A rule file cut between two rules has fewer rules to check, so compare the counts in the summary line (rules, names and series) with what you expect.

## Limits

- **A scrape shows only what that process serves right now.** Client libraries don't serve a labeled counter or histogram until it has a value, so a service that just started can miss metrics that appear after traffic. Hardware metrics (RAID, bonding, hwmon) appear only on hosts that have the hardware. Scrape after some traffic, give several targets, or use the Prometheus API.
- **Names and labels, not meaning.** It checks that what a query selects exists. It doesn't check label values, units, rates or whether the query is the right one.
- **Labels from relabeling.** Prometheus can add or rename labels when it scrapes. Pass those with `--target-label`; panelgap doesn't read scrape configs.
- **Its own PromQL parser.** It follows PromQL's grammar for selectors, functions, aggregations, operators, `or`, `and` and `unless`, offsets, `@` and subqueries, and refuses what it can't follow. It isn't Prometheus's parser, so a valid query it can't read is reported as a `not-checked` warning.
- **Grafana.** It reads dashboards from files. Library panels are listed as not checked. Mimir, Thanos and VictoriaMetrics datasources are treated as Prometheus, and so is a datasource it can't identify, which is Grafana's default. Datasource types come from the target, the panel, datasource variables and an exported dashboard's `__inputs`.

## FAQ

**Why does my Grafana panel show No data?** Often the metric was renamed or isn't scraped. panelgap compares each query with what the exporter serves and names the metric or label that's missing, plus similar names.

**Which metrics did node_exporter 0.16 rename?** Most of them: `node_cpu` became `node_cpu_seconds_total`, `node_memory_MemTotal` became `node_memory_MemTotal_bytes`, and so on. Run panelgap with a scrape of your exporter, and it lists each query that still uses an old name, with the name the exporter serves.

**How do I know if a Prometheus alert can ever fire?** If its expression selects a metric nobody serves, it can't. panelgap reads rule files and PrometheusRule YAML and reports those alerts, as in the node-exporter example above.

**Does it need Prometheus or Grafana running?** No. It reads files: a scrape you saved with `curl`, or a Prometheus API response, plus the dashboards and rules in your repository.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **Real dashboards, rules and exporters:** the 33 kube-prometheus v0.19.0 dashboards (read straight from its ConfigMap YAML) and its 8 rule files with 235 rules, against a node_exporter 1.12.1 scrape. Every query parses, the three Linux node_exporter dashboards are quiet, and the Kubernetes dashboards are reported, as there's no Kubernetes in the scrape. The Nodes dashboard against node_exporter 0.15.2 reports the 18 queries that use renamed metrics, and suggests the old name for the 15 that 0.15.2 serves. 0.15.2's diskstats collector fails on this kernel, so it serves no disk metrics, and those 3 get no suggestion. Prometheus 3.16.0's `/api/v1/label/__name__/values` and `/api/v1/series` outputs from scraping node_exporter 1.12.1 give the same results as the scrape.
- **By hand, not in `make test`:** six community dashboards from grafana.com (1860, 405, 6671, 11074, 3662, 315), which aren't in this repository because they have no stated license. All their queries parse. Dashboard 405 uses `new_name or old_name` fallbacks for node_exporter 0.15 and 1.x, and none of them is reported against either scrape. One of its Memory queries mixes a new and an old name in its first branch, so on 1.x neither branch returns data, and panelgap reports it against the 1.12.1 scrape. Against 0.15.2 it reports only the disk queries, since that version's diskstats collector fails here.
- **Input it can't trust:** tests pass 31 kinds of bad metrics input, 30 kinds of bad dashboard and 29 kinds of bad rule file, each with real files on the other sides, under every `--fail-on` and with `--format json`, and each must exit 2 with nothing on stdout. Bad files are also tried next to good ones and on stdin. Other tests cut the dashboard at every line break and every 97th byte (all refused), cut the scrapes at every line break and every 211th byte (mid-line cuts are refused; line cuts are refused or report every finding of the whole file with fewer series), and cut the rule file at every line break and every 53rd byte (mid-line cuts are refused; line cuts are refused or read with fewer rules).
- **The parser:** unit tests cover selectors, aggregations with `by` and `without`, binary operators with `on`, `ignoring` and `group_left`, offsets, `@`, subqueries, `absent()`, Grafana variables, comments, and how `or`, `and` and `unless` decide what's reported.
- **Go versions:** the tests pass on Go 1.22.12 and Go 1.27.3 on Linux arm64, offline. CI runs them on Linux amd64 with Go 1.22 and the current release.

Not tested: macOS and Windows; Grafana's v2 dashboard schema (refused); dashboards exported by a running Grafana (the API wrapper is tested with a file built from the kube-prometheus dashboard); OpenMetrics output from a real exporter (the parser accepts `# EOF`, `_created` and exemplars, tested with hand-written lines); exporters other than node_exporter; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/panelgap/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `panelgap` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
