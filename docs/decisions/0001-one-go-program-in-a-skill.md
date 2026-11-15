# 1. One Go program in a skill

Date: 2026-11-15

## Decision
panelgap is a Claude Code skill with one Go program in `plugin/scripts`, using only the standard library and a copy of go.yaml.in/yaml/v2 renamed to `package main`. The skill builds it with `go build` from the plugin directory, and people can `go run` it from the module.

## Why
The work is reading files and comparing names, which needs no running service, so an MCP server would add a process and a package without giving the agent anything a script can't. Copying the YAML package keeps `go build` working from the installed plugin, which has no `go.mod`, and offline.

## Alternatives
Prometheus's own PromQL parser would be exact, but brings in the Prometheus module and its dependencies and needs a newer Go than 1.22. See 0003.
