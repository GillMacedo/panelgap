// Package tests runs panelgap as a binary, the way the skill does, against
// real dashboards, rules and scrapes in examples/ and testdata/.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "panelgap-tests")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "panelgap")
	cmd := exec.Command("go", "build", "-o", bin, "./plugin/scripts")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building panelgap: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// runTool runs panelgap from the repository root.
func runTool(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, File, Source, Where, Kind, Metric, Label, Message, Fix string
	Similar                                                                []string
}

type jsonReport struct {
	Findings   []jsonFinding
	Dashboards []struct {
		Path, Title string
		Queries     int
	}
	Rules []struct {
		Path          string
		Groups, Rules int
	}
	Metrics []struct {
		Path, Format  string
		Names, Series int
	}
	Queries, Selectors int
	Names              int `json:"metric_names"`
}

func runJSON(t *testing.T, stdin []byte, args ...string) (jsonReport, result) {
	t.Helper()
	r := runTool(t, stdin, append([]string{"--format", "json"}, args...)...)
	var rep jsonReport
	if r.code == 2 {
		return rep, r
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("bad JSON (%v):\n%s\nstderr: %s", err, r.stdout, r.stderr)
	}
	return rep, r
}

// count returns how many findings have the code and severity.
func count(rep jsonReport, sev, code string) int {
	n := 0
	for _, f := range rep.Findings {
		if f.Severity == sev && f.Code == code {
			n++
		}
	}
	return n
}

func metricsOf(rep jsonReport, sev, code string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range rep.Findings {
		if f.Severity == sev && f.Code == code && !seen[f.Metric] {
			seen[f.Metric] = true
			out = append(out, f.Metric)
		}
	}
	return out
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Paths of the real inputs, relative to the repository root.
const (
	ex        = "examples/node-exporter/"
	scrapeNew = ex + "node_exporter-1.12.1.prom"
	scrapeOld = ex + "node_exporter-0.15.2.prom"
	namesAPI  = ex + "prometheus-3.16.0-names.json"
	seriesAPI = "tests/testdata/prometheus/prometheus-3.16.0-series.json"
	nodesDash = ex + "nodes.json"
	useDash   = ex + "node-rsrc-use.json"
	nodeRules = ex + "node-exporter-rules.yaml"
	kp        = "tests/testdata/kube-prometheus/"
)

var _ = strings.TrimSpace

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
