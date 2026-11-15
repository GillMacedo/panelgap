package tests

import (
	"strings"
	"testing"
)

// findingKeys returns one key per finding, for comparing runs.
func findingKeys(rep jsonReport) map[string]bool {
	out := map[string]bool{}
	for _, f := range rep.Findings {
		out[f.Severity+" "+f.Code+" "+f.Source+" "+f.Where+" "+f.Metric+" "+f.Label] = true
	}
	return out
}

// A dashboard is JSON, so a cut anywhere, on a line break or not, makes it
// unreadable.
func TestCutDashboard(t *testing.T) {
	data := readFile(t, nodesDash)
	dir := t.TempDir()
	cuts := 0
	for i := 1; i < len(data)-1; i++ {
		if i%97 != 0 && data[i-1] != '\n' {
			continue
		}
		cuts++
		p := writeFile(t, dir, "cut.json", data[:i])
		r := runTool(t, nil, "--metrics", scrapeNew, "--fail-on", "never", p)
		if r.code != 2 || r.stdout != "" {
			t.Fatalf("cut at byte %d of %d: exit %d\n%s", i, len(data), r.code, r.stdout)
		}
	}
	t.Logf("%d cuts refused", cuts)
}

// A scrape cut mid-line is refused. A cut on a line break is a valid,
// shorter scrape, which no tool can tell apart. Losing names can only add
// findings, never remove one, so such a cut must report everything the
// whole scrape does, and say it read fewer series.
func TestCutScrape(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{scrapeNew, scrapeOld} {
		data := readFile(t, path)
		full, r := runJSON(t, nil, "--metrics", path, "--target-label", "cluster", nodesDash, useDash, "--rules", nodeRules)
		if r.code == 2 {
			t.Fatal(r.stderr)
		}
		want := findingKeys(full)
		mid, line := 0, 0
		for i := 1; i < len(data)-1; i++ {
			onLine := data[i-1] == '\n'
			if !onLine && i%211 != 0 {
				continue
			}
			p := writeFile(t, dir, "cut.prom", data[:i])
			rep, r := runJSON(t, nil, "--metrics", p, "--target-label", "cluster", "--fail-on", "never", nodesDash, useDash, "--rules", nodeRules)
			if !onLine {
				mid++
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut mid-line at byte %d: exit %d", path, i, r.code)
				}
				continue
			}
			line++
			if r.code == 2 {
				continue
			}
			got := findingKeys(rep)
			for k := range want {
				if !got[k] {
					t.Fatalf("%s cut on a line break at byte %d lost the finding %s", path, i, k)
				}
			}
			if rep.Metrics[0].Series >= full.Metrics[0].Series {
				t.Fatalf("%s cut at byte %d reports %d series, the whole file %d", path, i, rep.Metrics[0].Series, full.Metrics[0].Series)
			}
		}
		t.Logf("%s: %d mid-line cuts refused, %d line cuts checked", path, mid, line)
	}
}

// A rule file cut mid-line is refused, since it has no final line break. A
// cut on a line break is refused when it leaves a rule or group unfinished
// or an expr unreadable. A cut between two rules is a valid shorter file;
// then the summary counts fewer rules than the whole file.
func TestCutRules(t *testing.T) {
	data := readFile(t, nodeRules)
	dir := t.TempDir()
	refused, shorter := 0, 0
	for i := 1; i < len(data)-1; i++ {
		onLine := data[i-1] == '\n'
		if !onLine && i%53 != 0 {
			continue
		}
		p := writeFile(t, dir, "cut.yaml", data[:i])
		rep, r := runJSON(t, nil, "--metrics", scrapeNew, "--fail-on", "never", "--rules", p)
		if !onLine {
			if r.code != 2 || r.stdout != "" {
				t.Fatalf("cut mid-line at byte %d: exit %d", i, r.code)
			}
			refused++
			continue
		}
		if r.code == 2 {
			refused++
			continue
		}
		if len(rep.Rules) != 1 || rep.Rules[0].Rules >= 41 {
			// The only valid cut with all 41 rules is one inside the last
			// rule's annotations, after its expr.
			if !strings.Contains(string(data[i:]), "expr:") && rep.Rules[0].Rules == 41 {
				continue
			}
			t.Fatalf("cut at byte %d: read %+v rules, want fewer than 41", i, rep.Rules)
		}
		shorter++
	}
	t.Logf("%d cuts refused, %d read as a shorter file", refused, shorter)
}
