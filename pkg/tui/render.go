package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// Typed value rendering for the inspector and detail pages: classify a record
// value (entity id, uid, timestamp, duration, number, boolean, URL, JSON) and
// render it styled, carrying the navigation target when the value is
// traversable. Classification is display-only — the raw value stays available
// for yank.

// entityIDRe matches both ID eras — Smartscape ("K8S_POD-16HEX") and classic
// ("CLOUD_APPLICATION-16HEX") ids share the TYPE-16HEX shape.
var entityIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$`)

// hexIDRe matches opaque hex identifiers: span ids (16) and trace ids (32).
var hexIDRe = regexp.MustCompile(`^[0-9a-fA-F]{16}$|^[0-9a-fA-F]{32}$`)

// traceIDRe matches a trace id as rendered by Grail (32 lowercase hex).
var traceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// valueView is the styled rendering of one record value plus the navigation
// target it carries (nil/"" = plain data).
type valueView struct {
	lines   []string        // styled display lines, wrapped to width
	compact string          // one-line preview for the collapsed layout ("" = lines[0])
	block   bool            // render expanded by default (JSON objects read as blocks)
	doc     any             // decoded JSON document — expands into per-line rows
	entity  *catalog.Entity // set when the value is a traversable entity id
	trace   string          // set when the value is a trace id
	raw     string          // unstyled text for yank
}

// entityFromID derives the entity behind an id value; the type is the id's
// prefix (both eras encode it there). Name is unknown until the detail fetch.
func entityFromID(id string) *catalog.Entity {
	i := strings.LastIndex(id, "-")
	if i <= 0 {
		return nil
	}
	return &catalog.Entity{ID: id, Type: id[:i]}
}

// renderValue renders a record value at the given content width.
func renderValue(key string, val any, width int) valueView {
	switch v := val.(type) {
	case nil:
		return valueView{lines: []string{theme.NullVal.Render("∅")}, raw: ""}
	case bool:
		return valueView{lines: []string{theme.Boolean.Render(strconv.FormatBool(v))}, raw: strconv.FormatBool(v)}
	case float64:
		text := catalog.FormatValue(v)
		return valueView{lines: []string{theme.Number.Render(groupDigits(text))}, raw: text}
	case string:
		return renderString(key, v, width)
	case map[string]any:
		lines := jsonLines(v, 0, width)
		return valueView{lines: lines, compact: rawJSON(v), raw: rawJSON(v),
			block: len(lines) <= maxAutoExpandLines, doc: v}
	case []any:
		return valueView{lines: jsonLines(v, 0, width), compact: rawJSON(v), raw: rawJSON(v), doc: v}
	default:
		text := fmt.Sprintf("%v", v)
		return valueView{lines: []string{text}, raw: text}
	}
}

func renderString(key, s string, width int) valueView {
	raw := s
	switch {
	case s == "":
		return valueView{lines: []string{theme.NullVal.Render(`""`)}, raw: raw}

	case entityIDRe.MatchString(s):
		return valueView{lines: []string{theme.Link.Render(s)}, entity: entityFromID(s), raw: raw}

	case strings.Contains(strings.ToLower(key), "trace") && traceIDRe.MatchString(s):
		return valueView{lines: []string{theme.UID.Render(s)}, trace: s, raw: raw}

	case isTimestamp(s):
		return valueView{lines: []string{renderTimestamp(s)}, raw: raw}

	case isDurationKey(key) && isDigits(s):
		return valueView{lines: []string{
			theme.Number.Render(catalog.FormatNs(s)) + theme.Dim.Render(" ("+groupDigits(s)+" ns)"),
		}, raw: raw}

	case isNumeric(s):
		// Identifier-ish keys (aws.account.id, span_id) are labels, not
		// quantities — no separators, no number color.
		if isIDKey(key) {
			return valueView{lines: []string{theme.UID.Render(s)}, raw: raw}
		}
		return valueView{lines: []string{theme.Number.Render(groupDigits(s))}, raw: raw}

	case hexIDRe.MatchString(s):
		return valueView{lines: []string{theme.UID.Render(s)}, raw: raw}

	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		return valueView{lines: wrapLines(theme.URL.Render(s), width), compact: theme.URL.Render(s), raw: raw}
	}

	// A string holding a JSON document (log content, k8s manifests) renders as
	// a highlighted block instead of an escaped one-liner.
	if doc := parseJSONDoc(s); doc != nil {
		_, isObj := doc.(map[string]any)
		lines := jsonLines(doc, 0, width)
		return valueView{lines: lines, compact: compactText(s), raw: raw,
			block: (isObj || blockString(key, s)) && len(lines) <= maxAutoExpandLines, doc: doc}
	}

	return valueView{lines: wrapLines(s, width), compact: compactText(s), raw: raw, block: blockString(key, s)}
}

// maxAutoExpandLines caps default expansion of JSON blocks: a k8s.object
// manifest renders to hundreds of lines and would swamp the record — huge
// docs start collapsed (enter expands; per-row navigation and paging make
// them workable).
const maxAutoExpandLines = 40

// blockString reports whether a plain string should render expanded by
// default: log content, multi-line text, and very long values read better
// wrapped than behind a ▸ preview.
func blockString(key, s string) bool {
	return key == "content" || strings.ContainsRune(s, '\n') || len(s) > 160
}

// compactText squashes a value onto one line for the collapsed preview.
func compactText(s string) string {
	if strings.ContainsAny(s, "\n\t") {
		return strings.Join(strings.Fields(s), " ")
	}
	return s
}

// wrapLines soft-wraps to width and strips the right-padding lipgloss adds —
// padded lines would defeat the inspector's inline-fit check.
func wrapLines(s string, width int) []string {
	lines := strings.Split(wrap(s, width), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// isTimestamp reports whether s is an ISO timestamp (RFC3339-ish, as Grail
// serializes them). Short strings are rejected fast to keep the common case
// cheap.
func isTimestamp(s string) bool {
	if len(s) < 19 || s[4] != '-' {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

// renderTimestamp shows the absolute local time plus how long ago it was:
// "2026-07-06 14:32:05 · 23m ago".
func renderTimestamp(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return iso
	}
	out := t.Local().Format("2006-01-02 15:04:05")
	if since := time.Since(t); since >= 0 {
		out += theme.Dim.Render(" · " + catalog.FormatDuration(since) + " ago")
	}
	return out
}

func isDurationKey(key string) bool {
	key = strings.ToLower(key)
	return key == "duration" || strings.HasSuffix(key, ".duration") || strings.HasSuffix(key, "_duration")
}

func isIDKey(key string) bool {
	key = strings.ToLower(key)
	return key == "id" || strings.HasSuffix(key, ".id") || strings.HasSuffix(key, "_id")
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// groupDigits adds thousands separators to the integer part of a plain
// number ("5194272" → "5,194,272"); anything unexpected passes through.
func groupDigits(s string) string {
	intPart, rest := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, rest = s[:i], s[i:]
	}
	neg := strings.HasPrefix(intPart, "-")
	digits := strings.TrimPrefix(intPart, "-")
	if !isDigits(digits) || len(digits) <= 4 {
		return s
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if neg {
		out = "-" + out
	}
	return out + rest
}

// parseJSONDoc parses a string that looks like a JSON object/array; nil when
// it is not one.
func parseJSONDoc(s string) any {
	t := strings.TrimSpace(s)
	if len(t) < 2 || (t[0] != '{' && t[0] != '[') {
		return nil
	}
	var doc any
	if err := json.Unmarshal([]byte(t), &doc); err != nil {
		return nil
	}
	if _, ok := doc.(map[string]any); ok {
		return doc
	}
	if _, ok := doc.([]any); ok {
		return doc
	}
	return nil
}

func rawJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// jsonLines renders a decoded JSON value as an indented, syntax-highlighted
// block (YAML-ish: keys and nesting carry the structure, no braces). Arrays
// of scalars render inline; arrays of objects get [i] index headers.
func jsonLines(v any, depth, width int) []string {
	var lines []string
	for _, n := range jsonRows(v, "", depth, width) {
		lines = append(lines, n.lines...)
	}
	return lines
}

// jsonNode is one selectable unit inside an expanded JSON block: a key or
// array element's own styled lines, its dotted path, and the yankable raw
// value (a scalar's text, or the compact JSON of a subtree).
type jsonNode struct {
	lines  []string
	path   string
	raw    string
	entity *catalog.Entity // set when the leaf is a traversable entity id
}

// jsonRows renders a decoded JSON value as one node per key/element so the
// inspector can make every line of an expanded block individually selectable
// and yankable. jsonLines flattens the same nodes into the plain block.
func jsonRows(v any, path string, depth, width int) []jsonNode {
	pad := strings.Repeat("  ", depth)
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return []jsonNode{{lines: []string{pad + theme.JSONPunct.Render("{}")}, path: path, raw: "{}"}}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var nodes []jsonNode
		for _, k := range keys {
			childPath := joinJSONPath(path, k)
			label := pad + theme.JSONKey.Render(k) + theme.JSONPunct.Render(":")
			if scalar, ok := jsonScalar(val[k]); ok {
				nodes = append(nodes, scalarNode(label+" "+scalar, childPath, val[k], width, depth))
			} else {
				nodes = append(nodes, jsonNode{lines: []string{label}, path: childPath, raw: rawJSON(val[k])})
				nodes = append(nodes, jsonRows(val[k], childPath, depth+1, width)...)
			}
		}
		return nodes
	case []any:
		if len(val) == 0 {
			return []jsonNode{{lines: []string{pad + theme.JSONPunct.Render("[]")}, path: path, raw: "[]"}}
		}
		// Arrays of entity ids (references) don't inline past one element:
		// each id gets its own navigable row instead of one opaque line.
		if len(val) == 1 || entityIDList(val) == nil {
			if inline, ok := inlineArray(val, width-lipgloss.Width(pad)); ok {
				n := jsonNode{lines: []string{pad + inline}, path: path, raw: rawJSON(val)}
				if s, ok := val[0].(string); ok && len(val) == 1 && entityIDRe.MatchString(s) {
					n.entity = entityFromID(s)
					n.raw = s
				}
				return []jsonNode{n}
			}
		}
		var nodes []jsonNode
		for i, e := range val {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			idx := pad + theme.JSONPunct.Render(fmt.Sprintf("[%d]", i))
			if scalar, ok := jsonScalar(e); ok {
				nodes = append(nodes, scalarNode(idx+" "+scalar, childPath, e, width, depth))
			} else {
				nodes = append(nodes, jsonNode{lines: []string{idx}, path: childPath, raw: rawJSON(e)})
				nodes = append(nodes, jsonRows(e, childPath, depth+1, width)...)
			}
		}
		return nodes
	default:
		scalar, _ := jsonScalar(v)
		return []jsonNode{scalarNode(pad+scalar, path, v, width, depth)}
	}
}

func joinJSONPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// scalarNode builds the node for one leaf line, carrying the unquoted raw
// value for yank and the entity link when the value is a traversable id.
func scalarNode(line, path string, v any, width, depth int) jsonNode {
	n := jsonNode{lines: wrapIndented(line, width, depth+1), path: path, raw: scalarRaw(v)}
	if s, ok := v.(string); ok && entityIDRe.MatchString(s) {
		n.entity = entityFromID(s)
	}
	return n
}

// scalarRaw is the yank text of a JSON leaf — strings unquoted, everything
// else as displayed.
func scalarRaw(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return catalog.FormatValue(val)
	case string:
		return val
	}
	return fmt.Sprintf("%v", v)
}

// jsonScalar renders a leaf value ("" and false when v nests).
func jsonScalar(v any) (string, bool) {
	switch val := v.(type) {
	case nil:
		return theme.NullVal.Render("null"), true
	case bool:
		return theme.Boolean.Render(strconv.FormatBool(val)), true
	case float64:
		return theme.Number.Render(catalog.FormatValue(val)), true
	case string:
		if entityIDRe.MatchString(val) {
			return theme.Link.Render(val), true
		}
		return theme.JSONStr.Render(strconv.Quote(val)), true
	}
	return "", false
}

// inlineArray renders an all-scalar array on one line when it fits.
func inlineArray(arr []any, width int) (string, bool) {
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := jsonScalar(e)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	line := theme.JSONPunct.Render("[") + strings.Join(parts, theme.JSONPunct.Render(", ")) + theme.JSONPunct.Render("]")
	if lipgloss.Width(line) > width {
		return "", false
	}
	return line, true
}

// wrapIndented wraps one styled line to width, indenting continuation lines
// one level deeper than the value's own depth.
func wrapIndented(line string, width, depth int) []string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return []string{line}
	}
	pad := strings.Repeat("  ", depth)
	parts := wrapLines(line, width-len(pad))
	for i := 1; i < len(parts); i++ {
		parts[i] = pad + parts[i]
	}
	return parts
}

// collectEntityIDs gathers every entity id in a record value — top-level,
// nested in arrays/objects, and inside JSON-document strings — for batched
// name resolution.
func collectEntityIDs(v any, into map[string]bool) {
	switch val := v.(type) {
	case string:
		if entityIDRe.MatchString(val) {
			into[val] = true
		} else if doc := parseJSONDoc(val); doc != nil {
			collectEntityIDs(doc, into)
		}
	case []any:
		for _, e := range val {
			collectEntityIDs(e, into)
		}
	case map[string]any:
		for _, e := range val {
			collectEntityIDs(e, into)
		}
	}
}

// entityIDList returns the elements of an all-entity-id array (problems carry
// affected_entity_ids arrays); nil when the value is anything else. These
// explode into one navigable inspector row per id.
func entityIDList(val any) []string {
	arr, ok := val.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	ids := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok || !entityIDRe.MatchString(s) {
			return nil
		}
		ids = append(ids, s)
	}
	return ids
}
