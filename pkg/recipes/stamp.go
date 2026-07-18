package recipes

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dynatrace-oss/dtctl/pkg/output"
)

// NewStamp builds a lastRun stamp for one execution outcome. LimitHit is
// derived from the last `| limit N` in the rendered DQL — a maxed count is
// weak signal and recorded distinctly.
func NewStamp(records int, seconds float64, provenance, partial, dql string) *LastRun {
	n := records
	lr := &LastRun{
		At:      time.Now().UTC().Format(time.RFC3339),
		Records: &n,
		Seconds: round2(seconds),
		Params:  provenance,
	}
	if limit, ok := lastLimit(dql); ok && n == limit {
		lr.LimitHit = true
	}
	if partial != "" {
		lr.Partial = firstLine(partial)
	}
	return lr
}

// UpdateStamp refreshes recipes.<name>.lastRun in the book file — the stamp
// is a living cache refreshed on every execution (RECIPES_CONCEPT.md §4.0).
// The write is a surgical yaml.Node patch (everything else in the file,
// including comments and unknown keys, is preserved) rendered to a temp file
// and atomically renamed. When resurrect is set the entry is also removed
// from disabled: — a disabled recipe that returns records comes back on the
// spot, never a one-way door.
//
// Stamp writes are best-effort by contract: callers turn an error into a
// warning (read-only deployments are first-class), never a query failure.
func UpdateStamp(path, name, source string, lr *LastRun, resurrect bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no recipe book to stamp at %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("recipe book %s does not parse: %w", path, err)
	}
	root := docRoot(&doc)
	if root == nil {
		return fmt.Errorf("recipe book %s has no top-level mapping", path)
	}

	recipesNode := findMapValue(root, "recipes")
	if recipesNode == nil {
		recipesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(root, "recipes", recipesNode)
	}
	entry := findMapValue(recipesNode, name)
	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if source != "" {
			src := &yaml.Node{}
			if err := src.Encode(source); err != nil {
				return err
			}
			setMapValue(entry, "source", src)
		}
		setMapValue(recipesNode, name, entry)
	}
	stamp := &yaml.Node{}
	if err := stamp.Encode(lr); err != nil {
		return err
	}
	setMapValue(entry, "lastRun", stamp)

	if resurrect {
		if disabled := findMapValue(root, "disabled"); disabled != nil {
			deleteMapKey(disabled, name)
		}
	}

	out, err := marshalNode(root)
	if err != nil {
		return err
	}
	_, err = output.WriteSpillFile(path, func(w io.Writer) error {
		_, werr := w.Write(out)
		return werr
	})
	return err
}

// WriteBook marshals a book and writes it atomically (temp file + rename).
func WriteBook(path string, book *Book) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(book); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	_, err := output.WriteSpillFile(path, func(w io.Writer) error {
		_, werr := w.Write(buf.Bytes())
		return werr
	})
	return err
}

func marshalNode(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// docRoot returns the top-level mapping node of a parsed document.
func docRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	if root := doc.Content[0]; root.Kind == yaml.MappingNode {
		return root
	}
	return nil
}

// findMapValue returns the value node for key in a mapping node, or nil.
func findMapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces key's value in a mapping node, appending when absent.
func setMapValue(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// deleteMapKey removes key (and its value) from a mapping node.
func deleteMapKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
