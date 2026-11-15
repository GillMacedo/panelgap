package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// metricSet is every series name the metrics inputs contain. For each name it
// keeps the label names seen, or nil when the input doesn't say (a list of
// names from the Prometheus API, or a recording rule).
type metricSet struct {
	labels  map[string]map[string]bool
	sources map[string]string // name -> the first input it came from
}

func newMetricSet() *metricSet {
	return &metricSet{labels: map[string]map[string]bool{}, sources: map[string]string{}}
}

// add records a name. labels nil means unknown; unknown wins over known,
// because then a label check could be wrong.
func (m *metricSet) add(name string, labels []string, known bool, source string) {
	cur, seen := m.labels[name]
	if _, ok := m.sources[name]; !ok {
		m.sources[name] = source
	}
	if !known {
		m.labels[name] = nil
		return
	}
	if seen && cur == nil {
		return
	}
	if cur == nil {
		cur = map[string]bool{}
		m.labels[name] = cur
	}
	for _, l := range labels {
		cur[l] = true
	}
}

func (m *metricSet) has(name string) bool {
	_, ok := m.labels[name]
	return ok
}

// metricsInput describes one parsed metrics input for the summary.
type metricsInput struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	Names  int    `json:"names"`
	// Series is the number of sample lines or series read.
	Series int `json:"series"`
}

var (
	metricNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRe  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// parseMetrics reads Prometheus text exposition (a /metrics scrape),
// OpenMetrics, or JSON from the Prometheus HTTP API: /api/v1/label/__name__/values
// or /api/v1/series. Anything else is refused.
func parseMetrics(path string, data []byte, set *metricSet) (metricsInput, error) {
	in := metricsInput{Path: path}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return in, fmt.Errorf("empty")
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		first := string(trimmed)
		if k := strings.IndexByte(first, '\n'); k >= 0 {
			first = first[:k]
		}
		if _, _, err := parseSample(strings.TrimSpace(first)); err != nil || json.Valid(trimmed) {
			return parseAPI(path, trimmed, set)
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return in, fmt.Errorf("binary data, not a metrics scrape")
	}
	return parseExposition(path, data, set)
}

func parseAPI(path string, data []byte, set *metricSet) (metricsInput, error) {
	in := metricsInput{Path: path}
	var resp struct {
		Status    string          `json:"status"`
		Data      json.RawMessage `json:"data"`
		ErrorType string          `json:"errorType"`
		Error     string          `json:"error"`
		Warnings  []string        `json:"warnings"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&resp); err != nil {
		return in, fmt.Errorf("not valid JSON (%v)", err)
	}
	if dec.More() {
		return in, fmt.Errorf("more than one JSON value")
	}
	if resp.Status != "success" {
		if resp.Error != "" {
			return in, fmt.Errorf("the Prometheus API returned an error: %s: %s", resp.ErrorType, resp.Error)
		}
		return in, fmt.Errorf(`JSON without "status": "success"; expected the output of /api/v1/label/__name__/values or /api/v1/series`)
	}
	var names []string
	if err := json.Unmarshal(resp.Data, &names); err == nil {
		in.Format = "label values API"
		for _, n := range names {
			if n == "" {
				return in, fmt.Errorf("empty metric name in data")
			}
			set.add(n, nil, false, path)
		}
		in.Names = len(names)
		if in.Names == 0 {
			return in, fmt.Errorf("the API returned no metric names")
		}
		return in, nil
	}
	var series []map[string]string
	if err := json.Unmarshal(resp.Data, &series); err != nil {
		return in, fmt.Errorf(`"data" is neither a list of names nor a list of series`)
	}
	in.Format = "series API"
	seen := map[string]bool{}
	for _, s := range series {
		n := s["__name__"]
		if n == "" {
			return in, fmt.Errorf("a series without __name__")
		}
		var ls []string
		for l := range s {
			if l != "__name__" {
				ls = append(ls, l)
			}
		}
		set.add(n, ls, true, path)
		seen[n] = true
		in.Series++
	}
	in.Names = len(seen)
	if in.Names == 0 {
		return in, fmt.Errorf("the API returned no series")
	}
	return in, nil
}

type family struct {
	name, typ string
	last      string // name of the last sample in the family
	samples   int
}

func parseExposition(path string, data []byte, set *metricSet) (metricsInput, error) {
	in := metricsInput{Path: path, Format: "text exposition"}
	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		return in, fmt.Errorf("no line break at the end, so the scrape looks cut off")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	names := map[string]bool{}
	var fam *family
	sawEOF := false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		n := i + 1
		if sawEOF {
			return in, fmt.Errorf("line %d: text after # EOF", n)
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] == "EOF" {
				in.Format = "OpenMetrics"
				sawEOF = true
				continue
			}
			if len(f) >= 3 && (f[1] == "HELP" || f[1] == "TYPE" || f[1] == "UNIT") {
				name := strings.Trim(f[2], `"`)
				if fam == nil || fam.name != name {
					fam = &family{name: name}
				}
				if f[1] == "TYPE" {
					if len(f) != 4 {
						return in, fmt.Errorf("line %d: # TYPE needs a name and a type: %s", n, clip(line))
					}
					switch f[3] {
					case "counter", "gauge", "histogram", "summary", "untyped", "unknown", "info", "stateset", "gaugehistogram":
					default:
						return in, fmt.Errorf("line %d: unknown metric type %q", n, f[3])
					}
					fam.typ = f[3]
				}
			}
			continue
		}
		name, labels, err := parseSample(line)
		if err != nil {
			return in, fmt.Errorf("line %d is not a metrics sample (%v): %s", n, err, clip(line))
		}
		set.add(name, labels, true, path)
		names[name] = true
		in.Series++
		if fam != nil && strings.HasPrefix(name, fam.name) {
			fam.samples++
			fam.last = name
		} else {
			fam = &family{name: name, samples: 1, last: name}
		}
	}
	if len(names) == 0 {
		return in, fmt.Errorf("no samples")
	}
	if fam != nil && fam.samples == 0 {
		return in, fmt.Errorf("the last metric, %s, has # HELP or # TYPE and no samples, so the scrape looks cut off", fam.name)
	}
	if fam != nil && (fam.typ == "histogram" || fam.typ == "summary" || fam.typ == "gaugehistogram") {
		if !strings.HasSuffix(fam.last, "_count") && !strings.HasSuffix(fam.last, "_sum") && !strings.HasSuffix(fam.last, "_created") && !strings.HasSuffix(fam.last, "_gcount") && !strings.HasSuffix(fam.last, "_gsum") {
			return in, fmt.Errorf("the last metric, %s %s, ends without its _sum or _count, so the scrape looks cut off", fam.typ, fam.name)
		}
	}
	in.Names = len(names)
	return in, nil
}

// parseSample reads one sample line and returns its name and label names.
func parseSample(line string) (string, []string, error) {
	i := 0
	name := ""
	for i < len(line) && line[i] != '{' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	name = line[:i]
	var labels []string
	if i < len(line) && line[i] == '{' {
		j, ls, qname, err := parseLabelSet(line, i)
		if err != nil {
			return "", nil, err
		}
		if qname != "" {
			if name != "" {
				return "", nil, fmt.Errorf("metric name given twice")
			}
			name = qname
		}
		labels = ls
		i = j
	} else if !metricNameRe.MatchString(name) {
		return "", nil, fmt.Errorf("%q is not a metric name", clip(name))
	}
	if name == "" {
		return "", nil, fmt.Errorf("no metric name")
	}
	if !metricNameRe.MatchString(name) && !strings.HasPrefix(line, "{") {
		return "", nil, fmt.Errorf("%q is not a metric name", clip(name))
	}
	rest := line[i:]
	if k := strings.Index(rest, " # "); k >= 0 {
		rest = rest[:k] // OpenMetrics exemplar
	}
	f := strings.Fields(rest)
	if len(f) < 1 || len(f) > 2 {
		return "", nil, fmt.Errorf("expected a value and an optional timestamp")
	}
	if _, err := strconv.ParseFloat(f[0], 64); err != nil {
		return "", nil, fmt.Errorf("%q is not a number", clip(f[0]))
	}
	if len(f) == 2 {
		if _, err := strconv.ParseFloat(f[1], 64); err != nil {
			return "", nil, fmt.Errorf("%q is not a timestamp", clip(f[1]))
		}
	}
	return name, labels, nil
}

// parseLabelSet reads {a="b",...} at line[i] and returns the index after it.
func parseLabelSet(line string, i int) (int, []string, string, error) {
	i++ // {
	var labels []string
	qname := ""
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == ',') {
			i++
		}
		if i >= len(line) {
			return 0, nil, "", fmt.Errorf("labels not closed")
		}
		if line[i] == '}' {
			return i + 1, labels, qname, nil
		}
		var key string
		if line[i] == '"' {
			s, j, err := readQuoted(line, i)
			if err != nil {
				return 0, nil, "", err
			}
			key, i = s, j
			if i < len(line) && (line[i] == ',' || line[i] == '}') {
				qname = key
				continue
			}
		} else {
			j := i
			for j < len(line) && line[j] != '=' && line[j] != '}' && line[j] != ',' {
				j++
			}
			key = strings.TrimSpace(line[i:j])
			if !labelNameRe.MatchString(key) {
				return 0, nil, "", fmt.Errorf("%q is not a label name", clip(key))
			}
			i = j
		}
		if i >= len(line) || line[i] != '=' {
			return 0, nil, "", fmt.Errorf("expected = after label %s", key)
		}
		i++
		if i >= len(line) || line[i] != '"' {
			return 0, nil, "", fmt.Errorf("label %s has no quoted value", key)
		}
		_, j, err := readQuoted(line, i)
		if err != nil {
			return 0, nil, "", err
		}
		i = j
		labels = append(labels, key)
	}
}

func readQuoted(line string, i int) (string, int, error) {
	var b strings.Builder
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			if j+1 < len(line) {
				b.WriteByte(line[j+1])
				j++
			}
		case '"':
			return b.String(), j + 1, nil
		default:
			b.WriteByte(line[j])
		}
	}
	return "", 0, fmt.Errorf("quoted value not closed")
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
