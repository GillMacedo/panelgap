package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type badInput struct {
	name    string
	content []byte // nil means use path as is
	path    string
}

func badCommon(t *testing.T) []badInput {
	scrape := readFile(t, scrapeNew)
	nodes := readFile(t, nodesDash)
	bin := make([]byte, 256)
	for i := range bin {
		bin[i] = byte(i)
	}
	return []badInput{
		{name: "empty", content: []byte{}},
		{name: "blank lines", content: []byte("\n\n  \n")},
		{name: "random text", content: []byte("hello world\nthis is not what you think\n")},
		{name: "binary", content: bin},
		{name: "go build failure", content: readFile(t, "tests/testdata/bad/go-build.txt")},
		{name: "go test failure", content: readFile(t, "tests/testdata/bad/go-test.txt")},
		{name: "node_exporter landing page", content: readFile(t, "tests/testdata/bad/ne-landing.html")},
		{name: "404 page", content: readFile(t, "tests/testdata/bad/ne-404.txt")},
		{name: "curl error", content: []byte("curl: (7) Failed to connect to localhost port 9100 after 0 ms: Couldn't connect to server\n")},
		{name: "kubectl error", content: []byte("error: the server doesn't have a resource type \"servicemonitors\"\n")},
		{name: "helm error", content: []byte("Error: execution error at (monitoring/templates/dashboards.yaml:12:4): grafana.enabled is required\n")},
		{name: "JSON of another kind", content: []byte(`{"name":"x","version":"1.0.0"}` + "\n")},
		{name: "JSON list", content: []byte("[1, 2, 3]\n")},
		{name: "scrape cut mid-line", content: scrape[:1000]},
		{name: "dashboard cut", content: nodes[:len(nodes)/2]},
		{name: "missing file", path: "does/not/exist.json"},
		{name: "a directory", path: "tests/testdata/bad"},
	}
}

func materialize(t *testing.T, dir string, b badInput, i int) string {
	if b.content == nil {
		return b.path
	}
	return writeFile(t, dir, "bad"+strings.Repeat("_", i)+".txt", b.content)
}

// mustRefuse runs args under every --fail-on and both formats, and wants
// exit 2, nothing on stdout, and the refusal on stderr.
func mustRefuse(t *testing.T, name string, stdin []byte, args ...string) {
	t.Helper()
	for _, fail := range []string{"error", "warn", "never"} {
		for _, format := range []string{"text", "json"} {
			all := append([]string{"--fail-on", fail, "--format", format}, args...)
			r := runTool(t, stdin, all...)
			if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input, nothing was checked") {
				t.Errorf("%s (--fail-on %s --format %s): exit %d, want 2 with nothing on stdout\nstdout: %.300s\nstderr: %s", name, fail, format, r.code, r.stdout, r.stderr)
				return
			}
		}
	}
}

// Each bad metrics input, with real dashboards and rules on the other side.
func TestBadMetrics(t *testing.T) {
	dir := t.TempDir()
	scrape := readFile(t, scrapeNew)
	names := readFile(t, namesAPI)
	extra := []badInput{
		{name: "Prometheus API error", content: readFile(t, "tests/testdata/bad/api-error.json")},
		{name: "Prometheus API with no names", content: readFile(t, "tests/testdata/bad/api-empty.json")},
		{name: "names API cut", content: names[:len(names)/2]},
		{name: "no final line break", content: scrape[:len(scrape)-1]},
		{name: "cut after # TYPE", content: cutAfter(t, scrape, "# TYPE node_load1 gauge\n")},
		{name: "cut inside a histogram", content: cutAfter(t, scrape, "go_gc_duration_seconds{quantile=\"0.5\"}")},
		{name: "cut mid-line then newline", content: append(append([]byte{}, scrape[:5000]...), '\n')},
		{name: "text after # EOF", content: []byte("up 1\n# EOF\nup 2\n")},
		{name: "a dashboard", content: readFile(t, nodesDash)},
		{name: "a rule file", content: readFile(t, nodeRules)},
		{name: "unknown type", content: []byte("# TYPE a countr\na 1\n")},
		{name: "a query API result", content: []byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`)},
		{name: "series API with no series", content: []byte(`{"status":"success","data":[]}`)},
		{name: "go test -json output", content: []byte("{\"Action\":\"start\",\"Package\":\"x\"}\n{\"Action\":\"fail\",\"Package\":\"x\"}\n")},
	}
	for i, b := range append(badCommon(t), extra...) {
		p := materialize(t, dir, b, i)
		mustRefuse(t, "metrics "+b.name, nil, "--metrics", p, "--rules", nodeRules, nodesDash)
		// The bad input among good ones still fails the run.
		mustRefuse(t, "metrics "+b.name+" after a good one", nil, "--metrics", scrapeNew, "--metrics", p, nodesDash)
		if b.content != nil {
			mustRefuse(t, "metrics "+b.name+" on stdin", b.content, "--metrics", "-", nodesDash)
		}
	}
}

// cutAfter returns data up to and including the first occurrence of s.
func cutAfter(t *testing.T, data []byte, s string) []byte {
	t.Helper()
	i := strings.Index(string(data), s)
	if i < 0 {
		t.Fatalf("%q not in data", s)
	}
	out := append([]byte{}, data[:i+len(s)]...)
	if !strings.HasSuffix(s, "\n") {
		// Cut on the line break after it, so only the structure is wrong.
		j := strings.IndexByte(string(data[i:]), '\n')
		out = append([]byte{}, data[:i+j+1]...)
	}
	return out
}

// Each bad dashboard, with a real scrape on the other side.
func TestBadDashboards(t *testing.T) {
	dir := t.TempDir()
	nodes := readFile(t, nodesDash)
	emptyDir := filepath.Join(dir, "empty")
	mkdir(t, emptyDir)
	txtDir := filepath.Join(dir, "txt")
	mkdir(t, txtDir)
	writeFile(t, txtDir, "readme.txt", []byte("dashboards go here\n"))
	extra := []badInput{
		{name: "Grafana API error", content: []byte(`{"message":"Dashboard not found","status":"not-found"}`)},
		{name: "Grafana v2 schema", content: []byte(`{"apiVersion":"dashboard.grafana.app/v2beta1","kind":"Dashboard","spec":{"title":"x","elements":{}}}`)},
		{name: "only a text panel", content: []byte(`{"title":"t","panels":[{"id":1,"type":"text","title":"hi"}]}`)},
		{name: "only Loki queries", content: []byte(`{"title":"t","panels":[{"id":1,"datasource":{"type":"loki"},"targets":[{"expr":"{app=\"x\"}"}]}]}`)},
		{name: "only unreadable queries", content: []byte(`{"title":"t","panels":[{"id":1,"targets":[{"expr":"rate(x[5m]"}]}]}`)},
		{name: "two JSON values", content: append(append([]byte{}, nodes...), nodes...)},
		{name: "a scrape", content: readFile(t, scrapeNew)},
		{name: "a rule file", content: readFile(t, nodeRules)},
		{name: "ConfigMap with cut JSON", content: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: d}\ndata:\n  d.json: '{\"title\": \"x\", \"panels\": ['\n")},
		{name: "ConfigMap without dashboards", content: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: d}\ndata:\n  a: b\n")},
		{name: "panels not a list of objects", content: []byte(`{"title":"t","panels":[1,2]}`)},
		{name: "empty directory", path: emptyDir},
		{name: "directory without .json", path: txtDir},
	}
	for i, b := range append(badCommon(t), extra...) {
		p := materialize(t, dir, b, i)
		mustRefuse(t, "dashboard "+b.name, nil, "--metrics", scrapeNew, p)
		if b.name != "only a text panel" && b.name != "only Loki queries" && b.name != "only unreadable queries" {
			mustRefuse(t, "dashboard "+b.name+" next to a good one", nil, "--metrics", scrapeNew, nodesDash, p)
			mustRefuse(t, "dashboard "+b.name+" next to rules", nil, "--metrics", scrapeNew, "--rules", nodeRules, p)
		}
		if b.content != nil {
			mustRefuse(t, "dashboard "+b.name+" on stdin", b.content, "--metrics", scrapeNew, "-")
		}
	}
}

// Each bad rule file, with a real scrape and dashboard on the other sides.
func TestBadRules(t *testing.T) {
	dir := t.TempDir()
	rules := string(readFile(t, nodeRules))
	exprAt := strings.Index(rules, "expr: |")
	extra := []badInput{
		{name: "no final line break", content: []byte(strings.TrimSuffix(rules, "\n"))},
		{name: "cut inside an expr", content: []byte(rules[:exprAt+40] + "\n")},
		{name: "cut before the last expr", content: []byte(rules[:strings.LastIndex(rules, "expr:")])},
		{name: "group without rules", content: []byte("groups:\n- name: a\n")},
		{name: "groups empty", content: []byte("groups: []\n")},
		{name: "record and alert", content: []byte("groups:\n- name: a\n  rules:\n  - record: x\n    alert: Y\n    expr: up\n")},
		{name: "neither record nor alert", content: []byte("groups:\n- name: a\n  rules:\n  - expr: up\n")},
		{name: "unrendered template", content: []byte("groups:\n- name: a\n  rules:\n  - alert: Down\n    expr: up{job=\"{{ .Values.job }}\"} == 0\n")},
		{name: "broken expr", content: []byte("groups:\n- name: a\n  rules:\n  - alert: Down\n    expr: sum(up\n")},
		{name: "a dashboard", content: readFile(t, nodesDash)},
		{name: "a scrape", content: readFile(t, scrapeNew)},
		{name: "PrometheusRule without groups", content: []byte("apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata: {name: x}\nspec: {}\n")},
	}
	for i, b := range append(badCommon(t), extra...) {
		p := materialize(t, dir, b, i)
		mustRefuse(t, "rules "+b.name, nil, "--metrics", scrapeNew, "--rules", p, nodesDash)
		mustRefuse(t, "rules "+b.name+" alone", nil, "--metrics", scrapeNew, "--rules", p)
		mustRefuse(t, "rules "+b.name+" after good rules", nil, "--metrics", scrapeNew, "--rules", nodeRules, "--rules", p)
	}
}

func TestBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{nodesDash},
		{"--metrics", scrapeNew},
		{"--metrics", scrapeNew, "--format", "xml", nodesDash},
		{"--metrics", scrapeNew, "--fail-on", "sometimes", nodesDash},
		{"--metrics", scrapeNew, "--target-label", "not a label", nodesDash},
		{"--metrics", scrapeNew, "--nope", nodesDash},
		{"--metrics", "-", "--rules", "-", nodesDash},
	} {
		r := runTool(t, []byte("up 1\n"), args...)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("%v: exit %d, stdout %q", args, r.code, r.stdout)
		}
	}
	if r := runTool(t, nil, "--version"); r.code != 0 || r.stdout != "panelgap 0.1.0\n" {
		t.Errorf("--version: %d %q", r.code, r.stdout)
	}
	_ = os.Getenv
}
