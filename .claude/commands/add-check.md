Add a check to panelgap for: $ARGUMENTS

1. Find a real dashboard, rule file or scrape where it matters, and say where it came from in the PR.
2. Add it in `plugin/scripts/check.go`, with a code in kebab case, a message that says what Grafana or Prometheus will do, and a fix line. Parser changes go in `plugin/scripts/promql.go`, with unit tests in `plugin/scripts/panelgap_test.go`.
3. Add a case to `tests/`, plus a case that must stay quiet. A new input format also needs its bad inputs in `tests/inputs_test.go`.
4. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-dashboards-against-metrics/SKILL.md`.
5. Run `make test`.
