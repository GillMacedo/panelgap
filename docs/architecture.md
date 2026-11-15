# Architecture

    flags --> parseMetrics (scrape, OpenMetrics, API JSON) --> metricSet: names and label names
          --> parseRules  --> recorded names into metricSet, one query per rule
          --> parseDashboardFile (JSON, API response, ConfigMaps) --> one query per target and variable
    each query --> replaceVars --> parseQuery --> selectors + tree
              --> checker: is each selector's metric there, with the labels its matchers need?
              --> blame(tree): which missing selectors make the query return nothing
    report --> text or JSON, exit 0, 1 or 2

- **The metric set** keeps, per name, the label names seen in the inputs, or "unknown" for names from the label values API and recording rules. Unknown labels are never reported.
- **The query tree** keeps only what decides whether a query returns data: every operand of arithmetic, comparisons, `and` and function arguments is needed; one branch of `or` is enough, unless it's only a constant; the right side of `unless` only filters; `absent()` always returns something. So `new or old` fallbacks aren't reported, and a branch that mixes new and old names is.
- **Grafana variables** become a placeholder rune before parsing. Matchers with one aren't judged, and names with one become patterns.
- **Refusal** is a separate error type. Any input that fails to parse, looks cut off, or is the output of a failed command ends the run with exit 2 before anything is printed on stdout.

Key choices are in `decisions/`.
