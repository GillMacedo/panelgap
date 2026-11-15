# 3. A small PromQL parser of its own

Date: 2026-11-15

## Decision
`promql.go` parses PromQL with a hand-written lexer and a recursive descent parser that keeps three levels of precedence: `or`, then `and` and `unless`, then everything else. It returns the vector selectors and a tree that says which of them the result depends on.

## Why
Prometheus's parser (`github.com/prometheus/prometheus/promql/parser`) is exact, but it pulls in a large module, needs a newer Go than the 1.22 this project supports, and can't read queries with Grafana variables such as `[$__rate_interval]` without rewriting them first. panelgap only needs selectors and the `or` and `unless` structure. A query it can't follow is reported as `not-checked` in a dashboard, and refused in a rule file, so a parser gap never turns into a clean result.

## Checked against
All 652 queries in the 33 kube-prometheus v0.19.0 dashboards and its 235 rules, and six community dashboards from grafana.com, parse.
