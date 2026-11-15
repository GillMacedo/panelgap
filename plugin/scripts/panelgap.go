// Command panelgap checks the PromQL in Grafana dashboards and Prometheus
// rule files against the metrics an exporter really serves, and reports
// queries that select metrics or labels that don't exist: panels that will
// show No data, variables with no values, and alerts that can't fire.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const version = "0.1.0"

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// untrusted is an input panelgap can't read, so it checks nothing.
type untrusted struct{ msg string }

func (u untrusted) Error() string { return u.msg }

func refuse(format string, a ...any) error { return untrusted{fmt.Sprintf(format, a...)} }

const usage = `usage: panelgap --metrics FILE [--metrics FILE...] [--rules FILE...] [DASHBOARD...]

Checks the PromQL in Grafana dashboards (JSON files, directories of them, or
Kubernetes YAML with dashboard ConfigMaps) and Prometheus rule files against
the metrics an exporter really serves, and reports queries that select
metric names or labels that aren't there.

  --metrics FILE       a /metrics scrape (Prometheus text or OpenMetrics), or
                       JSON from /api/v1/label/__name__/values or
                       /api/v1/series; "-" reads stdin. Repeat for more targets
  --rules FILE         a Prometheus rule file or PrometheusRule YAML. Its
                       recording rules count as metrics, and every rule's
                       expr is checked too
  --target-label NAME  a label Prometheus adds when it scrapes, so it's never
                       in a scrape (job and instance are always included).
                       Repeat, or separate with commas
  --format text|json   output format (default text)
  --fail-on LEVEL      error (default), warn or never: the lowest severity
                       that makes the exit code 1
  --version            print the version

Exit codes: 0 nothing at or above --fail-on, 1 findings at or above it,
2 an input can't be trusted or the flags are wrong (nothing was checked).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type report struct {
	Version    string         `json:"version"`
	Findings   []finding      `json:"findings"`
	Dashboards []dashInfo     `json:"dashboards"`
	Rules      []rulesInput   `json:"rules"`
	Metrics    []metricsInput `json:"metrics"`
	Queries    int            `json:"queries"`
	Selectors  int            `json:"selectors"`
	Names      int            `json:"metric_names"`
}

type dashInfo struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Queries int    `json:"queries"`
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("panelgap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var metrics, rules, targets listFlag
	fs.Var(&metrics, "metrics", "")
	fs.Var(&rules, "rules", "")
	fs.Var(&targets, "target-label", "")
	format := fs.String("format", "text", "")
	failOn := fs.String("fail-on", "error", "")
	showVersion := fs.Bool("version", false, "")
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")

	var dashboards []string
	for {
		if err := fs.Parse(args); err != nil {
			fmt.Fprintf(stderr, "panelgap: %v\n\n%s", err, usage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		dashboards = append(dashboards, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if *help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if *showVersion {
		fmt.Fprintln(stdout, "panelgap", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "panelgap: --format must be text or json, not %q\n", *format)
		return 2
	}
	threshold := map[string]int{"error": 3, "warn": 2, "never": 99}[*failOn]
	if threshold == 0 {
		fmt.Fprintf(stderr, "panelgap: --fail-on must be error, warn or never, not %q\n", *failOn)
		return 2
	}
	if len(metrics) == 0 {
		fmt.Fprintf(stderr, "panelgap: give at least one --metrics input\n\n%s", usage)
		return 2
	}
	if len(dashboards) == 0 && len(rules) == 0 {
		fmt.Fprintf(stderr, "panelgap: give a dashboard or --rules file to check\n\n%s", usage)
		return 2
	}
	tl := map[string]bool{"job": true, "instance": true}
	for _, t := range targets {
		for _, l := range strings.Split(t, ",") {
			l = strings.TrimSpace(l)
			if !labelNameRe.MatchString(l) {
				fmt.Fprintf(stderr, "panelgap: --target-label %q is not a label name\n", l)
				return 2
			}
			tl[l] = true
		}
	}

	rep, err := check(metrics, rules, dashboards, tl, stdin)
	if err != nil {
		var u untrusted
		if errors.As(err, &u) {
			fmt.Fprintf(stderr, "panelgap: can't trust the input, nothing was checked: %s\n", u.msg)
		} else {
			fmt.Fprintf(stderr, "panelgap: %v\n", err)
		}
		return 2
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		writeText(stdout, rep)
	}
	for _, f := range rep.Findings {
		if severityRank(f.Severity) >= threshold {
			return 1
		}
	}
	return 0
}

func severityRank(s string) int {
	switch s {
	case "ERROR":
		return 3
	case "WARN":
		return 2
	}
	return 1
}

func readInput(path string, stdin io.Reader, usedStdin *bool) ([]byte, error) {
	if path == "-" {
		if *usedStdin {
			return nil, refuse("stdin given more than once")
		}
		*usedStdin = true
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, refuse("stdin: %v", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, refuse("%v", err)
	}
	return data, nil
}

func check(metricPaths, rulePaths, dashPaths []string, targets map[string]bool, stdin io.Reader) (*report, error) {
	rep := &report{Version: version, Findings: []finding{}, Dashboards: []dashInfo{}, Rules: []rulesInput{}}
	set := newMetricSet()
	usedStdin := false
	for _, p := range metricPaths {
		data, err := readInput(p, stdin, &usedStdin)
		if err != nil {
			return nil, err
		}
		in, err := parseMetrics(label(p), data, set)
		if err != nil {
			return nil, refuse("%s: %v; is it the output of a failed command?", label(p), err)
		}
		rep.Metrics = append(rep.Metrics, in)
	}
	scraped := len(set.labels)
	for _, n := range prometheusMade {
		if !set.has(n) {
			set.add(n, nil, false, "Prometheus")
		}
	}
	var queries []query
	for _, p := range rulePaths {
		data, err := readInput(p, stdin, &usedStdin)
		if err != nil {
			return nil, err
		}
		qs, in, err := parseRules(label(p), data, set)
		if err != nil {
			return nil, refuse("%s: %v", label(p), err)
		}
		rep.Rules = append(rep.Rules, in)
		queries = append(queries, qs...)
	}
	var skips []skipped
	var files []string
	for _, p := range dashPaths {
		fs, err := expandDashboards(p)
		if err != nil {
			return nil, err
		}
		files = append(files, fs...)
	}
	for _, p := range files {
		data, err := readInput(p, stdin, &usedStdin)
		if err != nil {
			return nil, err
		}
		ds, err := parseDashboardFile(label(p), data)
		if err != nil {
			return nil, refuse("%s: %v", label(p), err)
		}
		for _, d := range ds {
			rep.Dashboards = append(rep.Dashboards, dashInfo{Path: label(p), Title: d.title, Queries: len(d.queries)})
			queries = append(queries, d.queries...)
			skips = append(skips, d.skipped...)
			if len(d.queries) == 0 {
				skips = append(skips, skipped{label(p), d.title, "dashboard", "it has no Prometheus queries"})
			}
		}
	}
	c := &checker{set: set, targetLabels: targets}
	for _, q := range queries {
		c.check(q)
	}
	for _, sk := range skips {
		c.add(finding{Severity: "note", Code: "not-checked", File: sk.file, Source: sk.source, Where: sk.where, Message: sk.reason + ", so it wasn't checked"})
	}
	if c.selectors == 0 {
		return nil, refuse("no query in the dashboards or rules selects a metric by name, so there was nothing to check")
	}
	sort.SliceStable(c.findings, func(i, j int) bool {
		return severityRank(c.findings[i].Severity) > severityRank(c.findings[j].Severity)
	})
	rep.Findings = c.findings
	rep.Queries, rep.Selectors, rep.Names = c.queries, c.selectors, scraped
	return rep, nil
}

func label(p string) string {
	if p == "-" {
		return "stdin"
	}
	return p
}

// expandDashboards turns a directory into the .json files under it.
func expandDashboards(p string) ([]string, error) {
	if p == "-" {
		return []string{p}, nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, refuse("%v", err)
	}
	if !st.IsDir() {
		return []string{p}, nil
	}
	var out []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != p && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, refuse("%v", err)
	}
	if len(out) == 0 {
		return nil, refuse("%s: a directory with no .json dashboards in it", p)
	}
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func writeText(w io.Writer, rep *report) {
	counts := map[string]int{}
	for _, f := range rep.Findings {
		counts[f.Severity]++
		head := f.Source
		if f.Where != "" {
			head += ", " + f.Where
		}
		fmt.Fprintf(w, "%s %s: %s\n", f.Severity, f.Code, head)
		fmt.Fprintf(w, "  %s\n", f.Message)
		if len(f.Similar) > 0 {
			fmt.Fprintf(w, "  similar names in the metrics inputs: %s\n", strings.Join(f.Similar, ", "))
		}
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
		fmt.Fprintf(w, "  in %s\n\n", f.File)
	}
	var from []string
	if len(rep.Dashboards) > 0 {
		from = append(from, plural(len(rep.Dashboards), "dashboard", "dashboards"))
	}
	if len(rep.Rules) > 0 {
		n := 0
		for _, r := range rep.Rules {
			n += r.Rules
		}
		from = append(from, plural(n, "rule", "rules"))
	}
	scope := fmt.Sprintf("%s (%s) from %s, against %s from %s",
		plural(rep.Queries, "query", "queries"), plural(rep.Selectors, "selector", "selectors"),
		strings.Join(from, " and "), plural(rep.Names, "metric name", "metric names"),
		plural(len(rep.Metrics), "metrics input", "metrics inputs"))
	if counts["ERROR"]+counts["WARN"] == 0 {
		fmt.Fprintf(w, "No problems in %s", scope)
		if counts["note"] > 0 {
			fmt.Fprintf(w, ", and %s above", plural(counts["note"], "note", "notes"))
		}
		fmt.Fprintln(w, ".")
	} else {
		fmt.Fprintf(w, "%s, %s and %s in %s.\n", plural(counts["ERROR"], "error", "errors"), plural(counts["WARN"], "warning", "warnings"), plural(counts["note"], "note", "notes"), scope)
	}
	for _, m := range rep.Metrics {
		if m.Series > 0 {
			fmt.Fprintf(w, "Metrics input %s: %s, %s in %s.\n", m.Path, m.Format, plural(m.Names, "name", "names"), plural(m.Series, "series", "series"))
		} else {
			fmt.Fprintf(w, "Metrics input %s: %s, %s.\n", m.Path, m.Format, plural(m.Names, "name", "names"))
		}
	}
}
