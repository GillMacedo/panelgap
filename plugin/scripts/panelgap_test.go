package main

import (
	"reflect"
	"strings"
	"testing"
)

func names(sels []selector) []string {
	var out []string
	for _, s := range sels {
		n := s.name
		if s.inAbsent {
			n = "absent:" + n
		}
		out = append(out, displayName(n))
	}
	return out
}

func TestParseSelectors(t *testing.T) {
	cases := []struct {
		q    string
		want []string
	}{
		{`up`, []string{"up"}},
		{`rate(http_requests_total{code=~"5.."}[5m])`, []string{"http_requests_total"}},
		{`sum by (job) (rate(a[5m])) / sum without (cpu) (b)`, []string{"a", "b"}},
		{`sum(rate(a[5m])) by (job)`, []string{"a"}},
		{`count without (cpu, mode) (node_cpu_seconds_total{mode="idle"})`, []string{"node_cpu_seconds_total"}},
		{`a / ignoring(cpu) group_left b`, []string{"a", "b"}},
		{`a * on(instance) group_left(nodename) node_uname_info`, []string{"a", "node_uname_info"}},
		{`histogram_quantile(0.99, sum by (le) (rate(x_bucket[$__rate_interval])))`, []string{"x_bucket"}},
		{`a offset 5m > bool 1e-3`, []string{"a"}},
		{`a @ start() and b @ 1609746000`, []string{"a", "b"}},
		{`max_over_time(a[1h:5m])`, []string{"a"}},
		{`absent(up{job="x"})`, []string{"absent:up"}},
		{`{__name__="foo", job="x"}`, []string{"foo"}},
		{`{"my.metric", a="b"}`, []string{"my.metric"}},
		{`node_${kind}_bytes`, []string{"node_$var_bytes"}},
		{`increase(a[$__range]) / $__range_s`, []string{"a"}},
		{`label_replace(a, "dst", "$1", "src", "(.*)")`, []string{"a"}},
		{`topk(5, a) or vector(0)`, []string{"a"}},
		{"a # comment\n + b", []string{"a", "b"}},
		{`-a ^ 2`, []string{"a"}},
		{`sum(a) BY (job) OR b`, []string{"a", "b"}},
		{`count_values("version", build_info)`, []string{"build_info"}},
		{`instance:node_cpu:rate5m{job="x"}`, []string{"instance:node_cpu:rate5m"}},
		{`time() - process_start_time_seconds`, []string{"process_start_time_seconds"}},
		{`a unless on(x) b`, []string{"a", "b"}},
		{`1 + Inf - NaN`, nil},
	}
	for _, c := range cases {
		sels, err := parseSelectors(replaceVars(c.q))
		if err != nil {
			t.Errorf("%s: %v", c.q, err)
			continue
		}
		if got := names(sels); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.q, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, q := range []string{
		``, `rate(a[5m]`, `a{b="c"`, `a{b=c}`, `sum by job (a)`, `a[5m`, `"unterminated`,
		`{app="x"} |= "error"`, `a ++`, `a b`, `)`, `sum(a))`, `a{b~"c"}`,
	} {
		if _, err := parseSelectors(replaceVars(q)); err == nil {
			t.Errorf("%q: parsed, want an error", q)
		}
	}
}

func TestYields(t *testing.T) {
	cases := []struct {
		q       string
		missing string // comma-separated names that don't exist
		yields  bool
		blamed  string
		filters string
	}{
		{`a`, "a", false, "a", ""},
		{`a + b`, "b", false, "b", ""},
		{`a or b`, "a", true, "", ""},
		{`a or b`, "a,b", false, "a,b", ""},
		{`(a - b) or (c - d)`, "b,c", false, "b,c", ""},
		{`(a - b) or (c - d)`, "c", true, "", ""},
		{`a and b`, "b", false, "b", ""},
		{`a unless b`, "b", true, "", "b"},
		{`a unless b unless c`, "a,c", false, "a", "c"},
		{`absent(a)`, "a", true, "", ""},
		{`sum(a) or vector(0)`, "a", true, "a", ""},
		{`count(a{x="1"}) or count(a2)`, "a2", true, "", ""},
		{`(a or b) * c`, "a,c", false, "c", ""},
	}
	for _, c := range cases {
		sels, root, err := parseQuery(c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.q, err)
		}
		miss := map[string]bool{}
		for _, m := range strings.Split(c.missing, ",") {
			miss[m] = true
		}
		ok := make([]bool, len(sels))
		for i, s := range sels {
			ok[i] = !miss[s.name]
		}
		if got := yields(root, ok); got != c.yields {
			t.Errorf("%s with %s missing: yields %v, want %v", c.q, c.missing, got, c.yields)
		}
		var b, f []int
		blame(root, ok, &b)
		emptyFilters(root, ok, &f)
		join := func(idx []int) string {
			var out []string
			for _, i := range idx {
				out = append(out, sels[i].name)
			}
			return strings.Join(out, ",")
		}
		if got := join(b); got != c.blamed {
			t.Errorf("%s with %s missing: blamed %q, want %q", c.q, c.missing, got, c.blamed)
		}
		if got := join(f); got != c.filters {
			t.Errorf("%s with %s missing: empty filters %q, want %q", c.q, c.missing, got, c.filters)
		}
	}
}

func TestRequiresLabel(t *testing.T) {
	cases := []struct {
		m    matcher
		want bool
	}{
		{matcher{label: "a", op: "=", value: "x"}, true},
		{matcher{label: "a", op: "=", value: ""}, false},
		{matcher{label: "a", op: "!=", value: "x"}, false},
		{matcher{label: "a", op: "=~", value: "x|y"}, true},
		{matcher{label: "a", op: "=~", value: ".*"}, false},
		{matcher{label: "a", op: "=~", value: "x|"}, false},
		{matcher{label: "a", op: "!~", value: "x"}, false},
		{matcher{label: "a", op: "=", value: "§", hasVar: true}, false},
	}
	for _, c := range cases {
		if got := c.m.requiresLabel(); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.m, got, c.want)
		}
	}
}

func TestSimilar(t *testing.T) {
	set := newMetricSet()
	for _, n := range []string{"node_cpu", "node_memory_MemTotal", "node_memory_Active_bytes", "node_disk_bytes_read", "node_disk_bytes_written", "go_goroutines"} {
		set.add(n, nil, true, "test")
	}
	c := &checker{set: set}
	cases := map[string]string{
		"node_cpu_seconds_total":              "node_cpu",
		"node_memory_MemTotal_bytes":          "node_memory_MemTotal",
		"node_disk_read_bytes_total":          "node_disk_bytes_read",
		"node_memory_HardwareCorrupted_bytes": "",
		"process_open_fds":                    "",
	}
	for missing, want := range cases {
		if got := strings.Join(c.similar(missing), ","); got != want {
			t.Errorf("similar(%s) = %q, want %q", missing, got, want)
		}
	}
}

func TestParseSample(t *testing.T) {
	good := []string{
		`up 1`, `a{b="c"} 1.5`, `a{b="c",} 2 1700000000000`, `a{b="x\"y"} NaN`, `a +Inf`,
		`{"my.metric",a="b"} 1`, `a_total 3 # {trace_id="x"} 1`,
	}
	for _, l := range good {
		if _, _, err := parseSample(l); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
	bad := []string{
		`404 page not found`, `<html>`, `up`, `up one`, `a{b="c" 1`, `a{b=c} 1`, `a{"b} 1`,
		`./main.go:3:1: syntax error`, `--- FAIL: TestX (0.00s)`, `ok  	example.com/x	0.01s`, `up 1 2 3`,
	}
	for _, l := range bad {
		if _, _, err := parseSample(l); err == nil {
			t.Errorf("%s: parsed, want an error", l)
		}
	}
}
