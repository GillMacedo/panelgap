# panelgap

Claude Code skill and Go CLI that checks the PromQL in Grafana dashboards and Prometheus rule files against the metrics an exporter really serves, and reports panels that will show No data and alerts that can't fire.

## Architecture
`plugin/scripts/` is one `package main` with no dependencies outside it. `panelgap.go` reads flags and inputs and prints the report. `metrics.go` reads scrapes and Prometheus API JSON, `dashboard.go` reads dashboards, `rules.go` reads rule files, `promql.go` parses PromQL into selectors and a tree that says which ones the result depends on, and `check.go` compares them. The `yaml_*.go` files are a copy of go.yaml.in/yaml/v2 renamed to `package main`. `plugin/skills/checking-dashboards-against-metrics/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet, unit tests and the binary tests in `tests/`
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run the examples

## Conventions
- Standard library only, plus the vendored YAML copy. No network access, ever: panelgap reads files.
- An input that can't be read completely exits 2 with nothing on stdout, under every `--fail-on`. Never skip a bad file silently. See `docs/decisions/0002-refuse-untrusted-input.md`.
- Each finding has a kebab-case code, a message that says what Grafana or Prometheus will do, and a fix line.
- README examples are real output. `tests/readme_test.go` runs every `$ ` line and compares.

## Do and don't
- Do try a new check on real dashboards and scrapes, such as the kube-prometheus files in `tests/testdata`, and read what it reports before adding it.
- Don't claim support for an input format or datasource without a test.

## Definition of done
`make test` and `make lint` pass, and `README.md`, `ROADMAP.md` and the skill are updated when relevant.
