package main

import (
	"fmt"
	"sort"
	"strings"
)

type finding struct {
	Severity string   `json:"severity"`
	Code     string   `json:"code"`
	File     string   `json:"file"`
	Source   string   `json:"source"`
	Where    string   `json:"where"`
	Kind     string   `json:"kind,omitempty"`
	Metric   string   `json:"metric,omitempty"`
	Label    string   `json:"label,omitempty"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Similar  []string `json:"similar,omitempty"`
}

// prometheusMade are series Prometheus writes itself, so they never appear
// in a scrape.
var prometheusMade = []string{
	"up", "scrape_duration_seconds", "scrape_samples_scraped", "scrape_samples_post_metric_relabeling",
	"scrape_series_added", "scrape_timeout_seconds", "scrape_sample_limit", "scrape_body_size_bytes",
	"ALERTS", "ALERTS_FOR_STATE",
}

type checker struct {
	set          *metricSet
	targetLabels map[string]bool
	findings     []finding
	selectors    int // selectors checked against the inputs
	queries      int // queries read without error
	order        int
}

func consequence(kind string) string {
	switch kind {
	case "variable":
		return "The variable gets no values from it, so panels that use the variable show No data"
	case "alert":
		return "The alert's expression selects nothing there, so it can't fire from that part"
	case "record":
		return "The rule records nothing from that part, and queries that use it get no data from it"
	}
	return "The query selects nothing there, so the panel shows No data, or misses those series"
}

func (c *checker) check(q query) {
	sels, root, err := parseQuery(replaceVars(q.expr))
	if err != nil {
		c.add(finding{Severity: "WARN", Code: "not-checked", File: q.file, Source: q.source, Where: q.where, Kind: q.kind,
			Message: fmt.Sprintf("panelgap couldn't read this query (%v), so it wasn't checked: %s", err, clip(oneLine(q.expr))),
			Fix:     "if it's PromQL that Prometheus accepts, please report it; if it's another datasource's query, set the target's datasource type"})
		return
	}
	c.queries++
	// A selector is ok when its metric exists and has every label its
	// matchers need. problems says why it isn't.
	ok := make([]bool, len(sels))
	problems := make([][]finding, len(sels))
	for i, sel := range sels {
		ok[i] = true
		if sel.name == "" {
			continue
		}
		if !sel.inAbsent {
			c.selectors++
		}
		if sel.pattern != nil {
			if !c.anyMatch(sel) {
				ok[i] = false
				problems[i] = append(problems[i], finding{Severity: "ERROR", Code: "missing-metric", Metric: displayName(sel.name),
					Message: fmt.Sprintf("no metric name in the metrics inputs or recording rules matches %s", displayName(sel.name)),
					Fix:     "check that the exporter that produces it is enabled and scraped, or change the query"})
			}
			continue
		}
		if !c.set.has(sel.name) {
			ok[i] = false
			f := finding{Severity: "ERROR", Code: "missing-metric", Metric: sel.name,
				Message: fmt.Sprintf("%s is not in any metrics input or recording rule", sel.name)}
			f.Similar = c.similar(sel.name)
			if len(f.Similar) > 0 {
				f.Fix = "if it's the same metric under another exporter version's name, query " + strings.Join(f.Similar, " or ") + " instead, or run the version the query expects"
			} else if strings.Contains(sel.name, ":") {
				f.Fix = "the colons suggest a recording rule; pass the rule file that records it with --rules"
			} else {
				f.Fix = "check that the exporter or collector that produces it is enabled and scraped, or remove the query"
			}
			problems[i] = append(problems[i], f)
			continue
		}
		for _, m := range sel.matchers {
			if !m.requiresLabel() {
				continue
			}
			if f, bad := c.labelProblem(sel.name, m.label, fmt.Sprintf("%s%s%q", m.label, m.op, m.value)); bad {
				ok[i] = false
				problems[i] = append(problems[i], f)
			}
		}
	}
	var blamed, filters []int
	blame(root, ok, &blamed)
	emptyFilters(root, ok, &filters)
	seen := map[string]bool{}
	report := func(idx []int, filter bool) {
		for _, i := range idx {
			if sels[i].inAbsent {
				continue
			}
			for _, f := range problems[i] {
				key := f.Code + " " + f.Metric + " " + f.Label
				if seen[key] {
					continue
				}
				seen[key] = true
				f.File, f.Source, f.Where, f.Kind = q.file, q.source, q.where, q.kind
				if filter {
					f.Severity = "WARN"
					f.Message += ". It's on the right of unless, so the filter removes nothing"
				} else if f.Code == "missing-metric" {
					f.Message += ". " + consequence(q.kind)
				} else {
					f.Message += "."
				}
				c.add(f)
			}
		}
	}
	report(blamed, false)
	report(filters, true)
	if q.label != "" && len(sels) == 1 && sels[0].pattern == nil && c.set.has(sels[0].name) {
		c.checkVariableLabel(q, sels[0].name, q.label)
	}
}

func (c *checker) labelHint(metric string) string {
	var have []string
	for l := range c.set.labels[metric] {
		have = append(have, l)
	}
	sort.Strings(have)
	if len(have) == 0 {
		return "it has no labels"
	}
	return "its labels are " + strings.Join(have, ", ")
}

// labelProblem reports a matcher that needs a label no series of the
// metric has. Labels Prometheus adds at scrape time are skipped.
func (c *checker) labelProblem(metric, label, used string) (finding, bool) {
	if c.targetLabels[label] || label == "__name__" {
		return finding{}, false
	}
	labels := c.set.labels[metric]
	if labels == nil || labels[label] {
		return finding{}, false
	}
	return finding{Severity: "WARN", Code: "missing-label", Metric: metric, Label: label,
		Message: fmt.Sprintf("no series of %s in the metrics inputs has the label %s, which %s needs (%s). Unless Prometheus adds %s when it scrapes, the selector matches nothing", metric, label, used, c.labelHint(metric), label),
		Fix:     fmt.Sprintf("if relabeling or external labels add %s, pass --target-label %s; otherwise use a label the exporter sets", label, label)}, true
}

func (c *checker) checkVariableLabel(q query, metric, label string) {
	f, bad := c.labelProblem(metric, label, "label_values(..., "+label+")")
	if !bad {
		return
	}
	f.File, f.Source, f.Where, f.Kind = q.file, q.source, q.where, q.kind
	if q.allMatchesEmpty != "" {
		f.Severity, f.Fix = "note", ""
		f.Message = fmt.Sprintf("no series of %s in the metrics inputs has the label %s (%s), so the variable gets no values. Its All value %q matches series without the label, so panels that use it still work", metric, label, c.labelHint(metric), q.allMatchesEmpty)
	} else {
		f.Message = fmt.Sprintf("no series of %s in the metrics inputs has the label %s (%s), so the variable gets no values and panels that use it show No data, unless Prometheus adds %s when it scrapes", metric, label, c.labelHint(metric), label)
	}
	c.add(f)
}

func (c *checker) anyMatch(sel selector) bool {
	for n := range c.set.labels {
		if sel.pattern.MatchString(n) {
			return true
		}
	}
	return false
}

func (c *checker) add(f finding) {
	c.findings = append(c.findings, f)
}

// similar suggests names from the inputs that share most of the missing
// name's words, such as node_cpu for node_cpu_seconds_total.
func (c *checker) similar(name string) []string {
	want := words(name)
	type cand struct {
		name  string
		score float64
	}
	var cs []cand
	for n, _ := range c.set.labels {
		if n == name {
			continue
		}
		have := words(n)
		if len(have) == 0 || len(want) == 0 || have[0] != want[0] {
			continue
		}
		score := jaccard(meaningful(want), meaningful(have))
		if strings.HasPrefix(n, name+"_") || strings.HasPrefix(name, n+"_") {
			score += 0.3
		}
		if score >= 0.6 {
			cs = append(cs, cand{n, score})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].score != cs[j].score {
			return cs[i].score > cs[j].score
		}
		return cs[i].name < cs[j].name
	})
	var out []string
	for i := 0; i < len(cs) && i < 3; i++ {
		out = append(out, cs[i].name)
	}
	return out
}

func words(n string) []string {
	return strings.FieldsFunc(strings.ToLower(n), func(r rune) bool { return r == '_' || r == ':' })
}

// unitWords are suffixes that say little about which metric it is.
var unitWords = map[string]bool{"total": true, "seconds": true, "bytes": true, "count": true, "sum": true, "bucket": true, "info": true, "ratio": true, "created": true}

// meaningful drops the namespace (the first word) and unit words.
func meaningful(ws []string) []string {
	var out []string
	for i, w := range ws {
		if i == 0 || unitWords[w] {
			continue
		}
		out = append(out, w)
	}
	if len(out) == 0 {
		return ws
	}
	return out
}

func jaccard(a, b []string) float64 {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, w := range a {
		sa[w] = true
	}
	for _, w := range b {
		sb[w] = true
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
