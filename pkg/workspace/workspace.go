// Package workspace discovers and parses the per-project .dynatrace.yaml
// workspace file: committable defaults (filter segments, timeframe, initial
// view, preferred environment) that tools apply on top of the user's personal
// dtctl config. Unlike the local .dtctl.yaml (which replaces the global config
// wholesale), a workspace file carries no contexts, no credentials, and no
// executable keys by construction — it can only narrow what a session shows,
// never change what it can do. Consumed by dynatui; dtctl CLI adoption is
// planned.
package workspace

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the per-project workspace file, discovered by walking up from
// the working directory (the same walk as dtctl's local .dtctl.yaml).
const FileName = ".dynatrace.yaml"

// MaxSegments mirrors the Grail limit of filter segments per query.
const MaxSegments = 10

// Version is the highest schema version this build understands.
const Version = 1

// Workspace is the parsed .dynatrace.yaml. All fields are optional; unknown
// keys are ignored so older builds keep working with newer committed files.
// Values are used verbatim — no environment-variable expansion, because a
// committed file must not behave differently per machine.
type Workspace struct {
	Version     int       `yaml:"version,omitempty"`     // schema version; 0 (absent) and 1 accepted
	Environment string    `yaml:"environment,omitempty"` // preferred environment URL (context is picked by match)
	View        string    `yaml:"view,omitempty"`        // initial dynatui view (CLI argument wins)
	Timeframe   string    `yaml:"timeframe,omitempty"`   // default timeframe: "30m", "2h", "3d"
	Segments    []Segment `yaml:"segments,omitempty"`    // filter segments applied to every DQL query

	path string // discovered file path; never serialized
}

// Segment references a filter segment by name or UID, with optional variable
// bindings for segments that define variables. YAML accepts a bare string
// (the common case) or the map form:
//
//	segments:
//	  - payments-prod
//	  - segment: 4lpVjcpcsjd
//	    variables:
//	      environment: [production]
type Segment struct {
	Ref       string              `yaml:"segment"`
	Variables map[string][]string `yaml:"variables,omitempty"`
}

// UnmarshalYAML accepts the scalar shorthand ("- payments-prod") alongside
// the map form.
func (s *Segment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&s.Ref)
	}
	type plain Segment // shed the method to avoid recursion
	return node.Decode((*plain)(s))
}

// Path returns where the workspace file was loaded from.
func (w *Workspace) Path() string {
	return w.path
}

// Find walks up from the current working directory looking for FileName.
func Find() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return findFrom(cwd)
}

// findFrom walks from dir to the filesystem root, mirroring dtctl's local
// config discovery (config.findLocalConfigFrom).
func findFrom(dir string) (string, bool) {
	for {
		path := filepath.Join(dir, FileName)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Discover finds and loads the workspace file for the current working
// directory. Returns (nil, nil) when no file exists; an error means a file
// was found but is unusable — callers should warn and continue without it.
func Discover() (*Workspace, error) {
	path, ok := Find()
	if !ok {
		return nil, nil
	}
	return Load(path)
}

// Load reads, parses, and validates one workspace file.
func Load(path string) (*Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var w Workspace
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := w.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	w.path = path
	return &w, nil
}

// timeframeRe matches the supported timeframe labels: minutes, hours, days.
var timeframeRe = regexp.MustCompile(`^[0-9]+[mhd]$`)

func (w *Workspace) validate() error {
	if w.Version > Version {
		return fmt.Errorf("version %d is newer than this build supports (max %d)", w.Version, Version)
	}
	if len(w.Segments) > MaxSegments {
		return fmt.Errorf("%d segments listed — Grail applies at most %d per query", len(w.Segments), MaxSegments)
	}
	for i, s := range w.Segments {
		if strings.TrimSpace(s.Ref) == "" {
			return fmt.Errorf("segments[%d]: missing segment name or UID", i)
		}
		for name, values := range s.Variables {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("segments[%d] (%s): variable with empty name", i, s.Ref)
			}
			if len(values) == 0 {
				return fmt.Errorf("segments[%d] (%s): variable %q has no values", i, s.Ref, name)
			}
		}
	}
	if w.Timeframe != "" && !timeframeRe.MatchString(w.Timeframe) {
		return fmt.Errorf("timeframe %q: expected <n>m, <n>h, or <n>d", w.Timeframe)
	}
	if w.Environment != "" {
		if err := validateEnvironment(w.Environment); err != nil {
			return fmt.Errorf("environment %q: %w", w.Environment, err)
		}
	}
	return nil
}

// validateEnvironment accepts an http(s) URL or a bare hostname.
func validateEnvironment(env string) error {
	if strings.ContainsAny(env, " \t") {
		return fmt.Errorf("must not contain whitespace")
	}
	u, err := url.Parse(env)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		// Bare host form ("abc12345.apps.dynatrace.com").
		if strings.ContainsAny(env, "/@?") {
			return fmt.Errorf("expected an https:// URL or a bare hostname")
		}
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}
