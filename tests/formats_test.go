package tests

import (
	"strings"
	"testing"
)

// OpenMetrics with # EOF, _created and exemplars, and Prometheus 3's quoted
// UTF-8 names, hand-written.
func TestOtherFormats(t *testing.T) {
	dir := t.TempDir()
	om := writeFile(t, dir, "om.txt", []byte(`# TYPE a counter
# HELP a Requests.
a_total{code="200"} 1 # {trace_id="abc"} 1 1.7e9
a_created{code="200"} 1.7e9
# TYPE h histogram
h_bucket{le="1"} 1
h_bucket{le="+Inf"} 1
h_count 1
h_sum 0.5
{"my.metric", zone="a"} 3
# EOF
`))
	dash := writeFile(t, dir, "d.json", []byte(`{"title":"T","panels":[{"id":1,"title":"P","targets":[
{"refId":"A","expr":"sum by (code) (rate(a_total{code=\"200\"}[5m]))"},
{"refId":"B","expr":"histogram_quantile(0.9, rate(h_bucket[5m]))"},
{"refId":"C","expr":"{\"my.metric\", zone=\"a\"}"}]}]}`))
	rep, r := runJSON(t, nil, "--metrics", om, "--fail-on", "warn", dash)
	if r.code != 0 || len(rep.Findings) != 0 || rep.Metrics[0].Format != "OpenMetrics" {
		t.Fatalf("exit %d, %+v, %+v\n%s", r.code, rep.Findings, rep.Metrics, r.stderr)
	}
	bad := writeFile(t, dir, "bad.json", []byte(`{"title":"T","panels":[{"id":1,"title":"P","targets":[
{"refId":"A","expr":"rate(a_count[5m])"},
{"refId":"B","expr":"{\"other.metric\"}"},
{"refId":"C","expr":"a_total{zone=\"a\"}"}]}]}`))
	rep, r = runJSON(t, nil, "--metrics", om, bad)
	if r.code != 1 || count(rep, "ERROR", "missing-metric") != 2 || count(rep, "WARN", "missing-label") != 1 {
		t.Fatalf("exit %d, %+v", r.code, rep.Findings)
	}
}

// Exported dashboards with __inputs, a byte order mark, and a query
// panelgap can't parse among good ones.
func TestExportedDashboard(t *testing.T) {
	dir := t.TempDir()
	dash := writeFile(t, dir, "d.json", []byte("\xef\xbb\xbf"+`{"__inputs":[{"name":"DS_PROM","type":"datasource","pluginId":"prometheus"},{"name":"DS_LOGS","type":"datasource","pluginId":"loki"}],
"title":"Exported","panels":[
 {"id":1,"title":"Logs","datasource":"${DS_LOGS}","targets":[{"refId":"A","expr":"{job=\"x\"} |= \"error\" | logfmt"}]},
 {"id":2,"title":"Load","datasource":"${DS_PROM}","targets":[{"refId":"A","expr":"node_load1"},{"refId":"B","expr":"node_load1 |> 2"}]}]}`))
	rep, r := runJSON(t, nil, "--metrics", scrapeNew, dash)
	if r.code != 0 || len(rep.Findings) != 1 || rep.Findings[0].Code != "not-checked" || rep.Findings[0].Severity != "WARN" || rep.Findings[0].Where != `panel "Load" (id 2), query B` {
		t.Fatalf("exit %d, %+v\n%s", r.code, rep.Findings, r.stderr)
	}
	if r := runTool(t, nil, "--metrics", scrapeNew, "--fail-on", "warn", dash); r.code != 1 {
		t.Errorf("--fail-on warn: exit %d, want 1", r.code)
	}
	r = runTool(t, nil, "--metrics", scrapeNew, dash)
	if strings.Contains(r.stdout, "No problems") {
		t.Errorf("a query it couldn't read must not end in No problems:\n%s", r.stdout)
	}
}
