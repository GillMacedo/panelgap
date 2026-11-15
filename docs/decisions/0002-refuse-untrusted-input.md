# 2. Refuse input it can't trust

Date: 2026-11-15

## Decision
panelgap exits 2, with nothing on stdout, when any input can't be read completely: empty files, random text, the output of a failed `curl`, `go build`, `go test`, `kubectl` or `helm`, HTTP error pages, Prometheus API errors and empty results, JSON of another kind, files cut off partway, rule files with unfinished rules or exprs that don't parse, and runs where no query selects a metric. `--fail-on` doesn't change this.

## Why
A checker that prints "No problems" for a scrape it couldn't read, or a dashboard cut in half, is worse than none. Exit 2 can't be mistaken for success in CI or by an agent.

## Limits
A scrape or rule file cut exactly on a line break can be a valid shorter file. For scrapes this can only add findings. The summary prints how many names, series and rules were read, so a short read is visible.
