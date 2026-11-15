package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// yamlDocs decodes every document in a YAML stream into maps with string
// keys. Empty documents are skipped; anything that isn't a mapping is refused.
func yamlDocs(data []byte) ([]map[string]any, error) {
	dec := NewDecoder(bytes.NewReader(data))
	var docs []map[string]any
	for n := 1; ; n++ {
		var v interface{}
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("document %d: not valid YAML or JSON (%v)", n, err)
		}
		if v == nil {
			continue
		}
		m, ok := normalize(v).(map[string]any)
		if !ok {
			return nil, fmt.Errorf("document %d is %s, not a YAML mapping; is it the output of a failed command?", n, describe(v))
		}
		docs = append(docs, m)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no YAML documents")
	}
	return docs, nil
}

func describe(v interface{}) string {
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("the text %q", clip(t))
	case []interface{}:
		return "a list"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

func normalize(v interface{}) any {
	switch t := v.(type) {
	case map[interface{}]interface{}:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []interface{}:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	}
	return v
}

type rulesInput struct {
	Path   string `json:"path"`
	Groups int    `json:"groups"`
	Rules  int    `json:"rules"`
}

// parseRules reads a Prometheus rule file or PrometheusRule objects. Each
// recording rule adds a name, and every rule's expr is checked.
func parseRules(path string, data []byte, set *metricSet) ([]query, rulesInput, error) {
	in := rulesInput{Path: path}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, in, fmt.Errorf("empty")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, in, fmt.Errorf("binary data, not a rule file")
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, in, fmt.Errorf("no line break at the end, so the file looks cut off")
	}
	docs, err := yamlDocs(data)
	if err != nil {
		return nil, in, err
	}
	var groups []any
	var collect func(doc map[string]any) error
	collect = func(doc map[string]any) error {
		if g, ok := doc["groups"]; ok {
			list, ok := g.([]any)
			if !ok {
				return fmt.Errorf("groups isn't a list")
			}
			groups = append(groups, list...)
			return nil
		}
		kind := s(doc["kind"])
		if kind == "PrometheusRule" {
			spec, _ := doc["spec"].(map[string]any)
			list, ok := spec["groups"].([]any)
			if !ok {
				return fmt.Errorf("PrometheusRule %v has no spec.groups list", name(doc))
			}
			groups = append(groups, list...)
			return nil
		}
		if strings.HasSuffix(kind, "List") {
			items, _ := doc["items"].([]any)
			for _, it := range items {
				if im, ok := it.(map[string]any); ok {
					if err := collect(im); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, d := range docs {
		if err := collect(d); err != nil {
			return nil, in, err
		}
	}
	if len(groups) == 0 {
		return nil, in, fmt.Errorf("no rule groups: expected a Prometheus rule file with groups:, or PrometheusRule objects")
	}
	var qs []query
	for gi, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			return nil, in, fmt.Errorf("group %d isn't a mapping", gi+1)
		}
		gname := scalar(gm["name"])
		if gname == "" {
			return nil, in, fmt.Errorf("group %d has no name", gi+1)
		}
		rules, ok := gm["rules"].([]any)
		if !ok || len(rules) == 0 {
			return nil, in, fmt.Errorf("group %s has no rules; is the file cut off?", gname)
		}
		in.Groups++
		for ri, r := range rules {
			rm, ok := r.(map[string]any)
			if !ok {
				return nil, in, fmt.Errorf("group %s, rule %d isn't a mapping", gname, ri+1)
			}
			// YAML 1.1 reads names such as Y or 1 as booleans and numbers.
			rec, alert := scalar(rm["record"]), scalar(rm["alert"])
			expr := strings.TrimSpace(fmt.Sprint(valueOr(rm["expr"], "")))
			if (rec == "") == (alert == "") {
				return nil, in, fmt.Errorf("group %s, rule %d needs exactly one of record and alert", gname, ri+1)
			}
			if expr == "" {
				return nil, in, fmt.Errorf("group %s, rule %d (%s%s) has no expr; is the file cut off?", gname, ri+1, rec, alert)
			}
			if strings.Contains(expr, "{{") {
				return nil, in, fmt.Errorf("group %s, rule %s%s: expr has {{ }} in it, so it looks like an unrendered template", gname, rec, alert)
			}
			if _, err := parseSelectors(replaceVars(expr)); err != nil {
				return nil, in, fmt.Errorf("group %s, %s%s: expr can't be read (%v); Prometheus refuses a rule file with a broken expr too. Is the file cut off?", gname, rec, alert, err)
			}
			in.Rules++
			q := query{file: path, source: "group " + gname, expr: expr}
			if rec != "" {
				if !metricNameRe.MatchString(rec) {
					return nil, in, fmt.Errorf("group %s: %q is not a valid metric name to record", gname, rec)
				}
				set.add(rec, nil, false, path)
				q.kind, q.where = "record", "record "+rec
			} else {
				q.kind, q.where = "alert", "alert "+alert
			}
			qs = append(qs, q)
		}
	}
	return qs, in, nil
}

func valueOr(v any, def any) any {
	if v == nil {
		return def
	}
	return v
}

func name(doc map[string]any) string {
	md, _ := doc["metadata"].(map[string]any)
	return s(md["name"])
}

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
