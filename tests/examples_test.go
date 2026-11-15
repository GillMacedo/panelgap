package tests

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The Nodes dashboard from kube-prometheus v0.19.0 against the
// node_exporter it was written for must stay quiet, under every --fail-on.
func TestHealthyStaysQuiet(t *testing.T) {
	for _, metrics := range []string{scrapeNew, namesAPI, seriesAPI} {
		for _, fail := range []string{"error", "warn", "never"} {
			rep, r := runJSON(t, nil, "--metrics", metrics, "--target-label", "cluster", "--fail-on", fail, nodesDash)
			if r.code != 0 {
				t.Fatalf("%s, --fail-on %s: exit %d\n%s%s", metrics, fail, r.code, r.stdout, r.stderr)
			}
			if len(rep.Findings) != 0 {
				t.Errorf("%s: want no findings, got %+v", metrics, rep.Findings)
			}
			if rep.Queries != 19 || rep.Selectors != 24 {
				t.Errorf("%s: checked %d queries and %d selectors, want 19 and 24", metrics, rep.Queries, rep.Selectors)
			}
		}
	}
	// Without --target-label cluster, the hidden $cluster variable gets no
	// values, but its All value .* still matches, so it's only a note.
	rep, r := runJSON(t, nil, "--metrics", scrapeNew, "--fail-on", "warn", nodesDash)
	if r.code != 0 || len(rep.Findings) != 1 || rep.Findings[0].Severity != "note" || rep.Findings[0].Label != "cluster" {
		t.Fatalf("exit %d, findings %+v", r.code, rep.Findings)
	}
	// With the recording rules, the USE dashboard is quiet too.
	rep, r = runJSON(t, nil, "--metrics", scrapeNew, "--target-label", "cluster", "--rules", nodeRules, useDash)
	for _, f := range rep.Findings {
		if f.Kind == "panel" || f.Kind == "variable" {
			t.Errorf("USE dashboard with rules: %+v", f)
		}
	}
}

// node_exporter 0.16 renamed most metrics. The new dashboard against 0.15.2
// must report every renamed metric and suggest the old name.
func TestRenamedMetrics(t *testing.T) {
	rep, r := runJSON(t, nil, "--metrics", scrapeOld, "--target-label", "cluster", nodesDash)
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s", r.code, r.stderr)
	}
	want := map[string]string{
		"node_cpu_seconds_total":            "node_cpu",
		"node_memory_MemTotal_bytes":        "node_memory_MemTotal",
		"node_memory_MemFree_bytes":         "node_memory_MemFree",
		"node_memory_Buffers_bytes":         "node_memory_Buffers",
		"node_memory_Cached_bytes":          "node_memory_Cached",
		"node_memory_MemAvailable_bytes":    "node_memory_MemAvailable",
		"node_filesystem_size_bytes":        "node_filesystem_size",
		"node_filesystem_avail_bytes":       "node_filesystem_avail",
		"node_network_receive_bytes_total":  "node_network_receive_bytes",
		"node_network_transmit_bytes_total": "node_network_transmit_bytes",
		// 0.15.2's diskstats collector fails on this kernel
		// (node_scrape_collector_success{collector="diskstats"} 0), so it
		// serves no disk metrics to suggest.
		"node_disk_read_bytes_total":      "",
		"node_disk_written_bytes_total":   "",
		"node_disk_io_time_seconds_total": "",
	}
	got := map[string]string{}
	for _, f := range rep.Findings {
		if f.Code != "missing-metric" || f.Severity != "ERROR" {
			t.Errorf("unexpected finding %+v", f)
			continue
		}
		if len(f.Similar) > 0 {
			got[f.Metric] = f.Similar[0]
		} else {
			got[f.Metric] = ""
		}
	}
	for m, old := range want {
		s, ok := got[m]
		if !ok {
			t.Errorf("%s not reported", m)
			continue
		}
		if s != old {
			t.Errorf("%s: suggested %q, want %q", m, s, old)
		}
	}
	if len(got) != len(want) {
		var extra []string
		for m := range got {
			if _, ok := want[m]; !ok {
				extra = append(extra, m)
			}
		}
		t.Errorf("reported %d names, want %d; extra: %v", len(got), len(want), extra)
	}
	if r := runTool(t, nil, "--metrics", scrapeOld, "--fail-on", "never", nodesDash); r.code != 0 {
		t.Errorf("--fail-on never: exit %d", r.code)
	}
}

// The USE dashboard reads recording rules. Without the rule file every panel
// is reported, with a hint about recording rules.
func TestRecordingRules(t *testing.T) {
	rep, r := runJSON(t, nil, "--metrics", scrapeNew, "--target-label", "cluster", useDash)
	if r.code != 1 || count(rep, "ERROR", "missing-metric") != 10 {
		t.Fatalf("exit %d, %d errors, want 1 and 10", r.code, count(rep, "ERROR", "missing-metric"))
	}
	for _, f := range rep.Findings {
		if !strings.Contains(f.Metric, ":") || !strings.Contains(f.Fix, "--rules") {
			t.Errorf("want a recording rule hint: %+v", f)
		}
	}
}

// The node-exporter alerts for RAID, bonding and systemd select metrics
// that node_exporter only serves with that hardware or collector. On this
// scrape they can't fire, and nothing else in the rules is reported.
func TestAlertsThatCantFire(t *testing.T) {
	for _, metrics := range []string{scrapeNew, namesAPI, seriesAPI} {
		rep, r := runJSON(t, nil, "--metrics", metrics, "--rules", nodeRules)
		if r.code != 1 {
			t.Fatalf("%s: exit %d, want 1\n%s", metrics, r.code, r.stderr)
		}
		var alerts []string
		for _, f := range rep.Findings {
			if f.Kind != "alert" || f.Severity != "ERROR" {
				t.Errorf("%s: unexpected %+v", metrics, f)
			}
			alerts = append(alerts, strings.TrimPrefix(f.Where, "alert ")+" "+f.Metric)
		}
		sort.Strings(alerts)
		want := []string{
			"NodeBondingDegraded node_bonding_active",
			"NodeBondingDegraded node_bonding_slaves",
			"NodeRAIDDegraded node_md_disks",
			"NodeRAIDDegraded node_md_disks_required",
			"NodeRAIDDiskFailure node_md_disks",
			"NodeSystemdServiceCrashlooping node_systemd_service_restart_total",
			"NodeSystemdServiceFailed node_systemd_unit_state",
		}
		if !reflect.DeepEqual(alerts, want) {
			t.Errorf("%s: got\n%s\nwant\n%s", metrics, strings.Join(alerts, "\n"), strings.Join(want, "\n"))
		}
		if len(rep.Rules) != 1 || rep.Rules[0].Rules != 41 {
			t.Errorf("%s: rules read: %+v", metrics, rep.Rules)
		}
	}
}

// Every query in the 33 kube-prometheus dashboards and 235 rules (counted with grep for alert: and record: keys) parses,
// read straight from the ConfigMap and PrometheusRule YAML. The Kubernetes
// dashboards have nothing to match in a node_exporter scrape, so they are
// reported; the node_exporter ones aren't.
func TestKubePrometheusParses(t *testing.T) {
	args := []string{"--metrics", scrapeNew, "--target-label", "cluster", "--fail-on", "never"}
	for _, f := range []string{"alertmanager", "grafana", "kubePrometheus", "kubeStateMetrics", "kubernetesControlPlane", "nodeExporter", "prometheus", "prometheusOperator"} {
		args = append(args, "--rules", kp+f+"-prometheusRule.yaml")
	}
	args = append(args, kp+"grafana-dashboardDefinitions.yaml")
	rep, r := runJSON(t, nil, args...)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	if len(rep.Dashboards) != 33 {
		t.Errorf("read %d dashboards, want 33", len(rep.Dashboards))
	}
	rules := 0
	for _, ru := range rep.Rules {
		rules += ru.Rules
	}
	if rules != 235 {
		t.Errorf("read %d rules, want 235", rules)
	}
	for _, f := range rep.Findings {
		if f.Code == "not-checked" {
			t.Errorf("not checked: %+v", f)
		}
		// The MacOS and AIX dashboards select metrics node_exporter serves
		// only on those systems, so only the Linux ones must stay quiet.
		linux := f.Source == "Node Exporter / Nodes" || strings.HasPrefix(f.Source, "Node Exporter / USE Method")
		if linux && f.Severity != "note" {
			t.Errorf("node_exporter dashboard reported: %+v", f)
		}
	}
	if count(rep, "ERROR", "missing-metric") < 500 {
		t.Errorf("only %d missing-metric errors; the Kubernetes dashboards should report most queries", count(rep, "ERROR", "missing-metric"))
	}
}

// A dashboard as Grafana's API returns it, and a directory of dashboards,
// give the same result as the plain files.
func TestDashboardShapes(t *testing.T) {
	dir := t.TempDir()
	nodes := readFile(t, nodesDash)
	wrapped := writeFile(t, dir, "api.json", append(append([]byte(`{"meta":{"slug":"nodes"},"dashboard":`), nodes...), '}'))
	sub := dir + "/dir"
	mkdir(t, sub)
	writeFile(t, sub, "nodes.json", nodes)
	writeFile(t, sub, "notes.txt", []byte("not a dashboard"))
	for _, p := range []string{wrapped, sub} {
		rep, r := runJSON(t, nil, "--metrics", scrapeOld, "--target-label", "cluster", p)
		if r.code != 1 || count(rep, "ERROR", "missing-metric") != 18 {
			t.Errorf("%s: exit %d, %d errors, want 1 and 18\n%s", p, r.code, count(rep, "ERROR", "missing-metric"), r.stderr)
		}
	}
	// stdin for the scrape.
	rep, r := runJSON(t, readFile(t, scrapeOld), "--metrics", "-", "--target-label", "cluster", nodesDash)
	if r.code != 1 || count(rep, "ERROR", "missing-metric") != 18 {
		t.Errorf("stdin: exit %d, %d errors", r.code, count(rep, "ERROR", "missing-metric"))
	}
}

// Labels: a matcher on a label the exporter never sets is a warning, unless
// --target-label says Prometheus adds it.
func TestMissingLabel(t *testing.T) {
	dir := t.TempDir()
	dash := writeFile(t, dir, "d.json", []byte(`{"title":"T","panels":[{"id":1,"title":"P","datasource":{"type":"prometheus","uid":"x"},"targets":[
{"refId":"A","expr":"rate(node_cpu_seconds_total{mode=\"idle\", namespace=\"monitoring\"}[5m])"},
{"refId":"B","expr":"node_load1{nodename!=\"x\"}"},
{"refId":"C","expr":"node_load1{zone=~\".*\"}"},
{"refId":"D","expr":"node_load1{zone=\"a\"} or node_load1"}]}]}`))
	rep, r := runJSON(t, nil, "--metrics", scrapeNew, dash)
	if r.code != 0 || count(rep, "WARN", "missing-label") != 1 || rep.Findings[0].Label != "namespace" {
		t.Fatalf("exit %d, findings %+v", r.code, rep.Findings)
	}
	if r := runTool(t, nil, "--metrics", scrapeNew, "--fail-on", "warn", dash); r.code != 1 {
		t.Errorf("--fail-on warn: exit %d, want 1", r.code)
	}
	rep, r = runJSON(t, nil, "--metrics", scrapeNew, "--target-label", "namespace,zone", "--fail-on", "warn", dash)
	if r.code != 0 || len(rep.Findings) != 0 {
		t.Errorf("with --target-label: exit %d, %+v", r.code, rep.Findings)
	}
	// The names API has no labels, so labels aren't checked.
	rep, r = runJSON(t, nil, "--metrics", namesAPI, "--fail-on", "warn", dash)
	if r.code != 0 || len(rep.Findings) != 0 {
		t.Errorf("names API: exit %d, %+v", r.code, rep.Findings)
	}
}

// Non-Prometheus targets, library panels, rows and variables.
func TestDashboardParts(t *testing.T) {
	dir := t.TempDir()
	dash := writeFile(t, dir, "d.json", []byte(`{"title":"Parts",
"templating":{"list":[
 {"name":"ds","type":"datasource","query":"prometheus"},
 {"name":"logs","type":"datasource","query":"loki"},
 {"name":"host","type":"query","datasource":"$ds","query":"label_values(node_uname_info, nodename)"},
 {"name":"gone","type":"query","datasource":{"type":"prometheus"},"query":{"query":"label_values(gone_metric_info, version)","refId":"x"}},
 {"name":"all","type":"query","query":"label_names()"},
 {"name":"iv","type":"interval","query":"1m,5m"}]},
"rows":[{"panels":[{"id":7,"title":"Old row","targets":[{"expr":"node_load5"}]}]}],
"panels":[
 {"id":1,"title":"Logs","datasource":"$logs","targets":[{"refId":"A","expr":"{app=\"x\"} |= \"error\""}]},
 {"id":2,"title":"Mixed","datasource":{"type":"datasource","uid":"-- Mixed --"},"targets":[
   {"refId":"A","datasource":{"type":"loki"},"expr":"count_over_time({app=\"x\"}[5m])"},
   {"refId":"B","datasource":{"type":"prometheus"},"expr":"missing_from_mixed"},
   {"refId":"C","expr":"default_ds_in_mixed"}]},
 {"id":10,"title":"Dashboard source","datasource":{"type":"datasource","uid":"-- Dashboard --"},"targets":[{"refId":"A","expr":"never_checked"}]},
 {"id":3,"title":"Lib","libraryPanel":{"uid":"u","name":"Shared CPU"}},
 {"id":4,"title":"Row","type":"row","collapsed":true,"panels":[{"id":5,"title":"Nested","targets":[{"refId":"A","expr":"nested_missing_total"}]}]},
 {"id":6,"title":"Pattern","targets":[{"refId":"A","expr":"node_${kind}_bytes_total"},{"refId":"B","expr":"nothing_${x}_like_this"}]},
 {"id":8,"title":"Name regex","targets":[{"refId":"A","expr":"{__name__=~\"node_load.*\"}"},{"refId":"B","expr":"{__name__=~\"zzz_.*\"}"}]},
 {"id":9,"title":"Text","type":"text"}]}`))
	rep, r := runJSON(t, nil, "--metrics", scrapeNew, dash)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	got := metricsOf(rep, "ERROR", "missing-metric")
	sort.Strings(got)
	want := []string{"default_ds_in_mixed", "gone_metric_info", "missing_from_mixed", "nested_missing_total", "nothing_$var_like_this", "zzz_.*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missing: got %v, want %v", got, want)
	}
	notes := 0
	for _, f := range rep.Findings {
		if f.Severity == "note" {
			notes++
			if !strings.Contains(f.Message, "library panel") {
				t.Errorf("unexpected note %+v", f)
			}
		}
	}
	if notes != 1 {
		t.Errorf("%d notes, want 1 (the library panel)", notes)
	}
}
