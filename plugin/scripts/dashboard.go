package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// query is one PromQL expression to check, with where it came from.
type query struct {
	file   string
	source string // dashboard title, or rule group
	where  string // panel "CPU" (id 2), query A / variable $instance / alert X
	kind   string // panel, variable, record, alert
	expr   string
	// label is set for label_values(metric, label): the variable is empty
	// unless the metric has this label.
	label string
	// allMatchesEmpty is the variable's All value when it matches "".
	allMatchesEmpty string
}

// skipped is something in a dashboard that panelgap didn't check.
type skipped struct {
	file, source, where, reason string
}

type dashboardResult struct {
	title   string
	queries []query
	skipped []skipped
}

// parseDashboardFile reads a dashboard JSON file, a Grafana API response
// with a "dashboard" field, or Kubernetes YAML with ConfigMaps whose data
// holds dashboard JSON, as Grafana's sidecar and kube-prometheus use.
func parseDashboardFile(path string, data []byte) ([]dashboardResult, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary data, not a dashboard")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		d, err := parseDashboardJSON(path, trimmed)
		if err != nil {
			return nil, err
		}
		return []dashboardResult{d}, nil
	}
	return parseConfigMaps(path, data)
}

func parseDashboardJSON(path string, data []byte) (dashboardResult, error) {
	var res dashboardResult
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return res, fmt.Errorf("not valid dashboard JSON (%v)", err)
	}
	if dec.More() {
		return res, fmt.Errorf("more than one JSON value")
	}
	if inner, ok := raw["dashboard"].(map[string]any); ok {
		raw = inner
	}
	if _, ok := raw["elements"]; ok {
		return res, fmt.Errorf("this is a Grafana v2 schema dashboard, which panelgap doesn't read yet")
	}
	if spec, ok := raw["spec"].(map[string]any); ok {
		if _, ok := spec["elements"]; ok {
			return res, fmt.Errorf("this is a Grafana v2 schema dashboard, which panelgap doesn't read yet")
		}
		if _, ok := spec["panels"]; ok {
			raw = spec
		}
	}
	_, hasPanels := raw["panels"].([]any)
	_, hasRows := raw["rows"].([]any)
	if !hasPanels && !hasRows {
		return res, fmt.Errorf(`JSON without "panels" or "rows", so not a Grafana dashboard`)
	}
	res.title, _ = raw["title"].(string)
	if res.title == "" {
		res.title = "(untitled)"
	}
	d := &dashboardWalk{path: path, title: res.title, dsVars: map[string]string{}}
	// Exported dashboards name their datasources in __inputs, as ${DS_X}.
	if inputs, ok := raw["__inputs"].([]any); ok {
		for _, in := range inputs {
			if im, ok := in.(map[string]any); ok && s(im["type"]) == "datasource" {
				d.dsVars[s(im["name"])] = strings.ToLower(s(im["pluginId"]))
			}
		}
	}
	// Datasource variables say which type a ${datasource} panel uses.
	if tl, ok := raw["templating"].(map[string]any); ok {
		list, _ := tl["list"].([]any)
		for _, v := range list {
			vm, _ := v.(map[string]any)
			if vm == nil {
				return res, fmt.Errorf("templating.list has an entry that isn't an object")
			}
			if s(vm["type"]) == "datasource" {
				d.dsVars[s(vm["name"])] = strings.ToLower(s(vm["query"]))
			}
		}
		for _, v := range list {
			d.variable(v.(map[string]any))
		}
	}
	if hasPanels {
		if err := d.panels(raw["panels"].([]any), nil); err != nil {
			return res, err
		}
	}
	if hasRows {
		for i, r := range raw["rows"].([]any) {
			rm, ok := r.(map[string]any)
			if !ok {
				return res, fmt.Errorf("rows[%d] isn't an object", i)
			}
			ps, _ := rm["panels"].([]any)
			if err := d.panels(ps, nil); err != nil {
				return res, err
			}
		}
	}
	res.queries, res.skipped = d.queries, d.skipped
	return res, nil
}

type dashboardWalk struct {
	path, title string
	dsVars      map[string]string
	queries     []query
	skipped     []skipped
}

func s(v any) string {
	str, _ := v.(string)
	return str
}

var nonPrometheus = []string{"loki", "tempo", "elasticsearch", "influx", "graphite", "mysql", "postgres", "mssql", "cloudwatch", "jaeger", "zipkin", "testdata", "grafana", "__expr__", "dashboard", "opensearch", "clickhouse", "pyroscope", "splunk", "datadog", "stackdriver", "azure", "-- mixed --"}

// dsType returns the datasource type, "" when it can't be told (then a
// target with expr is assumed to be Prometheus, as Grafana's default).
func (d *dashboardWalk) dsType(ds any) string {
	switch v := ds.(type) {
	case map[string]any:
		t := strings.ToLower(s(v["type"]))
		if t == "datasource" {
			// Grafana's special sources: -- Mixed -- leaves the choice to
			// each target, -- Dashboard -- and -- Grafana -- run no PromQL.
			if strings.Contains(strings.ToLower(s(v["uid"])), "mixed") {
				return ""
			}
			return "grafana"
		}
		if t != "" {
			return t
		}
		return d.dsType(v["uid"])
	case string:
		name := strings.TrimSpace(v)
		if m := grafanaVar.FindString(name); m == name && m != "" {
			key := strings.Trim(m, "${}[]")
			if k := strings.IndexByte(key, ':'); k >= 0 {
				key = key[:k]
			}
			return d.dsVars[key]
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "prometheus") || strings.Contains(lower, "mimir") || strings.Contains(lower, "thanos") {
			return "prometheus"
		}
		for _, t := range nonPrometheus {
			if strings.Contains(lower, t) {
				return t
			}
		}
	}
	return ""
}

func isPrometheusType(t string) bool {
	return t == "" || t == "prometheus" || strings.Contains(t, "prometheus") || t == "mimir" || t == "thanos" || t == "victoriametrics-datasource" || t == "-- mixed --"
}

func (d *dashboardWalk) panels(ps []any, parentDS any) error {
	for i, p := range ps {
		pm, ok := p.(map[string]any)
		if !ok {
			return fmt.Errorf("a panel (number %d in its list) isn't an object", i+1)
		}
		title := s(pm["title"])
		where := fmt.Sprintf("panel %q", title)
		if id, ok := pm["id"].(float64); ok {
			where = fmt.Sprintf("panel %q (id %d)", title, int(id))
		}
		if lp, ok := pm["libraryPanel"].(map[string]any); ok {
			if _, has := pm["targets"]; !has {
				d.skipped = append(d.skipped, skipped{d.path, d.title, where, fmt.Sprintf("library panel %q; its queries live in Grafana, not in this file", s(lp["name"]))})
				continue
			}
		}
		ds := pm["datasource"]
		if ds == nil {
			ds = parentDS
		}
		if targets, ok := pm["targets"].([]any); ok {
			for j, t := range targets {
				tm, ok := t.(map[string]any)
				if !ok {
					return fmt.Errorf("%s: target %d isn't an object", where, j+1)
				}
				tds := tm["datasource"]
				if tds == nil {
					tds = ds
				}
				ref := s(tm["refId"])
				if ref == "" {
					ref = fmt.Sprint(j + 1)
				}
				w := where + ", query " + ref
				typ := d.dsType(tds)
				expr, hasExpr := tm["expr"].(string)
				if !isPrometheusType(typ) {
					continue
				}
				if !hasExpr {
					if typ != "" && strings.Contains(typ, "prometheus") {
						d.skipped = append(d.skipped, skipped{d.path, d.title, w, "Prometheus target with no expr"})
					}
					continue
				}
				if strings.TrimSpace(expr) == "" {
					continue
				}
				d.queries = append(d.queries, query{file: d.path, source: d.title, where: w, kind: "panel", expr: expr})
			}
		}
		if sub, ok := pm["panels"].([]any); ok {
			if err := d.panels(sub, ds); err != nil {
				return err
			}
		}
	}
	return nil
}

var (
	labelValuesRe = regexp.MustCompile(`(?s)^\s*label_values\s*\((.*),\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)\s*$`)
	queryResultRe = regexp.MustCompile(`(?s)^\s*query_result\s*\((.*)\)\s*$`)
	otherVarFnRe  = regexp.MustCompile(`(?s)^\s*(label_values|label_names|metrics)\s*\(`)
)

func (d *dashboardWalk) variable(vm map[string]any) {
	if s(vm["type"]) != "query" {
		return
	}
	name := s(vm["name"])
	where := "variable $" + name
	if !isPrometheusType(d.dsType(vm["datasource"])) {
		return
	}
	var q string
	switch v := vm["query"].(type) {
	case string:
		q = v
	case map[string]any:
		q = s(v["query"])
	}
	if strings.TrimSpace(q) == "" {
		return
	}
	if m := labelValuesRe.FindStringSubmatch(q); m != nil {
		qq := query{file: d.path, source: d.title, where: where, kind: "variable", expr: m[1], label: m[2]}
		// With no values, Grafana's All option uses allValue, and a regex
		// such as .* still matches series that lack the label.
		if all := s(vm["allValue"]); vm["includeAll"] == true && all != "" {
			if re, err := regexp.Compile("^(?:" + all + ")$"); err == nil && re.MatchString("") {
				qq.allMatchesEmpty = all
			}
		}
		d.queries = append(d.queries, qq)
		return
	}
	if m := queryResultRe.FindStringSubmatch(q); m != nil {
		d.queries = append(d.queries, query{file: d.path, source: d.title, where: where, kind: "variable", expr: m[1]})
		return
	}
	if otherVarFnRe.MatchString(q) {
		// label_values(label), label_names() and metrics(regex) select no
		// metric by name.
		return
	}
	d.queries = append(d.queries, query{file: d.path, source: d.title, where: where, kind: "variable", expr: q})
}

// parseConfigMaps reads Kubernetes YAML and returns the dashboards in the
// .json keys of its ConfigMaps.
func parseConfigMaps(path string, data []byte) ([]dashboardResult, error) {
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, fmt.Errorf("not dashboard JSON, and as YAML it has no line break at the end, so it looks cut off")
	}
	docs, err := yamlDocs(data)
	if err != nil {
		return nil, err
	}
	var out []dashboardResult
	var walk func(doc map[string]any) error
	walk = func(doc map[string]any) error {
		kind := s(doc["kind"])
		if strings.HasSuffix(kind, "List") {
			items, _ := doc["items"].([]any)
			for _, it := range items {
				im, ok := it.(map[string]any)
				if !ok {
					return fmt.Errorf("a List item isn't an object")
				}
				if err := walk(im); err != nil {
					return err
				}
			}
			return nil
		}
		if kind != "ConfigMap" {
			return nil
		}
		md, _ := doc["metadata"].(map[string]any)
		cm := s(md["name"])
		dm, _ := doc["data"].(map[string]any)
		for key, v := range dm {
			if !strings.HasSuffix(key, ".json") {
				continue
			}
			str, ok := v.(string)
			if !ok {
				return fmt.Errorf("ConfigMap %s, key %s isn't a string", cm, key)
			}
			d, err := parseDashboardJSON(path, []byte(strings.TrimSpace(str)))
			if err != nil {
				return fmt.Errorf("ConfigMap %s, key %s: %v", cm, key, err)
			}
			out = append(out, d)
		}
		return nil
	}
	for _, doc := range docs {
		if err := walk(doc); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("not dashboard JSON, and no ConfigMap in it has a .json key with a dashboard")
	}
	sortDashboards(out)
	return out, nil
}

func sortDashboards(ds []dashboardResult) {
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j].title < ds[j-1].title; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
}
