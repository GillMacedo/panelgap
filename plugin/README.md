# panelgap plugin

This directory is the Claude Code plugin for panelgap, which checks the PromQL in Grafana dashboards and Prometheus rule files against the metrics an exporter really serves. `skills/checking-dashboards-against-metrics/SKILL.md` is the skill, and `scripts/` is the Go program it builds. See the [repository README](https://github.com/GillMacedo/panelgap#readme) for examples and limits.

Install it with:

    claude plugin marketplace add GillMacedo/panelgap
    claude plugin install panelgap@panelgap-marketplace
