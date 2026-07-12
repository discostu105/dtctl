package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dynatrace-oss/dtctl/pkg/resources/analyzer"
	"github.com/dynatrace-oss/dtctl/pkg/resources/anomalydetector"
	"github.com/dynatrace-oss/dtctl/pkg/resources/segment"
	"github.com/dynatrace-oss/dtctl/pkg/resources/slo"
	"github.com/dynatrace-oss/dtctl/sdk/session"
	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// tuiSources wires the TUI's API-backed views (catalog.Spec.API) to dtctl's
// resource handlers. All sources are read-only; construction here keeps
// internal/tui free of HTTP, mirroring how main.go passes the DQL executor.
func tuiSources(c *session.Client) map[string]tui.Source {
	return map[string]tui.Source{
		"slos":              sloSource(slo.NewHandler(c)),
		"anomaly-detectors": detectorSource(anomalydetector.NewHandler(c)),
		"log-patterns":      logPatternSource(analyzer.NewHandler(c)),
	}
}

// segmentSource adapts the segment resource handler to the TUI's picker and
// workspace seeding (the handler's List already requests the VARIABLES
// add-field). Construction here keeps internal/tui free of HTTP.
func segmentSource(h *segment.Handler) tui.SegmentLister {
	return func(_ context.Context) ([]tui.SegmentOption, error) {
		list, err := h.List()
		if err != nil {
			return nil, err
		}
		opts := make([]tui.SegmentOption, 0, len(list.FilterSegments))
		for _, s := range list.FilterSegments {
			opt := tui.SegmentOption{
				UID:          s.UID,
				Name:         s.Name,
				Description:  s.Description,
				HasVariables: s.Variables != nil,
			}
			if s.Variables != nil && s.Variables.Type == "query" {
				opt.VariablesQuery = s.Variables.Value
			}
			opts = append(opts, opt)
		}
		sort.SliceStable(opts, func(i, j int) bool {
			return strings.ToLower(opts[i].Name) < strings.ToLower(opts[j].Name)
		})
		return opts, nil
	}
}

// sloEvalConcurrency bounds the parallel per-SLO evaluation fan-out.
const sloEvalConcurrency = 6

// sloEvalTimeout caps one SLO's evaluation polling.
const sloEvalTimeout = 12 * time.Second

// sloSource lists SLO definitions and evaluates each one in parallel — the
// SLO API returns definitions only (no status/value/error budget), so the
// live view runs the evaluation endpoint per SLO and merges the result of
// the first criteria into the record. Evaluation failures degrade to blank
// status cells, never errors: the list is the primary content.
func sloSource(h *slo.Handler) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
		list, err := h.List("", 400)
		if err != nil {
			return nil, err
		}
		// One shared budget bounds the whole evaluation fan-out — on a
		// large tenant the list must not hide behind minutes of stragglers;
		// SLOs past the budget just show blank status cells.
		ctx, cancel := context.WithTimeout(ctx, 2*sloEvalTimeout)
		defer cancel()
		records := make([]map[string]any, len(list.SLOs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, sloEvalConcurrency)
		for i := range list.SLOs {
			s := list.SLOs[i]
			rec := map[string]any{
				"id":          s.ID,
				"name":        s.Name,
				"description": s.Description,
				"tags":        anySlice(s.Tags),
			}
			if len(s.Criteria) > 0 {
				rec["target"] = s.Criteria[0].Target
				if s.Criteria[0].Warning != nil {
					rec["warning"] = *s.Criteria[0].Warning
				}
				rec["window"] = strings.TrimPrefix(s.Criteria[0].TimeframeFrom, "now-")
			}
			if ts := slo.DecodeVersionTimestamp(s.Version); ts != nil {
				rec["modified"] = ts.Format(time.RFC3339)
			}
			if indicator, ok := s.CustomSli["indicator"].(string); ok {
				rec["indicator"] = indicator
			}
			records[i] = rec
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				mergeSLOEvaluation(ctx, h, rec, s.ID)
			}()
		}
		wg.Wait()
		return records, nil
	}
}

// mergeSLOEvaluation runs one SLO's evaluation (with polling) and folds the
// first result into the record. Errors leave the record untouched.
func mergeSLOEvaluation(ctx context.Context, h *slo.Handler, rec map[string]any, id string) {
	resp, err := h.Evaluate(id)
	if err != nil {
		return
	}
	// Poll state stays in a local: responses may rotate the token, and
	// mutating the response to carry it forward would smuggle loop state
	// into server data.
	token := resp.EvaluationToken
	deadline := time.Now().Add(sloEvalTimeout)
	for len(resp.EvaluationResults) == 0 {
		// The poll is a server-side long-poll — clamp it to the remaining
		// budget or a poll issued just before the deadline overshoots it.
		remaining := time.Until(deadline)
		if token == "" || remaining <= 0 || ctx.Err() != nil {
			return
		}
		pollMs := int(remaining / time.Millisecond)
		if pollMs > 5000 {
			pollMs = 5000
		}
		next, err := h.PollEvaluation(token, pollMs)
		if err != nil {
			return
		}
		if next.EvaluationToken != "" {
			token = next.EvaluationToken
		}
		resp = next
	}
	res := resp.EvaluationResults[0]
	rec["status"] = res.Status
	if res.Value != nil {
		rec["sli"] = *res.Value
	}
	if res.ErrorBudget != nil {
		rec["errorBudget"] = *res.ErrorBudget
	}
	if res.Message != "" {
		rec["message"] = res.Message
	}
}

// detectorSource lists Davis anomaly detectors (Settings API), flattened for
// the table with the raw settings value kept for the inspector.
func detectorSource(h *anomalydetector.Handler) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
		detectors, err := h.List(anomalydetector.ListOptions{})
		if err != nil {
			return nil, err
		}
		records := make([]map[string]any, 0, len(detectors))
		for _, d := range detectors {
			records = append(records, map[string]any{
				"title":       d.Title,
				"enabled":     strconv.FormatBool(d.Enabled),
				"analyzer":    d.AnalyzerShort,
				"eventType":   d.EventType,
				"source":      d.Source,
				"description": d.Description,
				"objectId":    d.ObjectID,
				"value":       d.Value,
			})
		}
		return records, nil
	}
}

// logPatternTimeout caps one pattern extraction end to end (execute + poll).
const logPatternTimeout = 120

// logPatternSource executes the Davis log-pattern analyzer over the composed
// logs query (dql — scope and server searches already injected by the view)
// and returns one record per extracted pattern, sorted by match count.
func logPatternSource(h *analyzer.Handler) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
		if strings.TrimSpace(dql) == "" {
			return nil, fmt.Errorf("log-pattern extraction needs a logs query")
		}
		input := map[string]any{
			// LogPatternInput appends the timestamp/content projection the
			// analyzer schema requires — after the composed pipeline, so
			// injected facet stages still see the full record.
			"logQuery":         catalog.LogPatternInput(dql),
			"numberOfExamples": catalog.LogPatternExamples,
			"generalParameters": map[string]any{
				// The logQuery carries its own from:, but the analyzer's
				// default analysis window is 2h — align both to the view's
				// timeframe so neither clips the other.
				"timeframe": map[string]any{
					"startTime": "now-" + scope.Timeframe.Label,
					"endTime":   "now",
				},
			},
		}
		result, err := h.ExecuteAndWait(ctx, catalog.LogPatternAnalyzer, input, logPatternTimeout)
		if err != nil {
			return nil, err
		}
		if result.Result == nil {
			return nil, fmt.Errorf("analyzer returned no result")
		}
		if result.Result.ExecutionStatus != "COMPLETED" {
			return nil, fmt.Errorf("analyzer execution %s", strings.ToLower(result.Result.ExecutionStatus))
		}
		records := make([]map[string]any, 0, len(result.Result.Output))
		for _, item := range result.Result.Output {
			records = append(records, map[string]any(item))
		}
		// The analyzer returns patterns unsorted (validated live) — busiest
		// patterns first is what triage reads.
		sort.SliceStable(records, func(i, j int) bool {
			return patternMatches(records[i]) > patternMatches(records[j])
		})
		return records, nil
	}
}

func patternMatches(rec map[string]any) float64 {
	// FloatValue coerces both JSON numbers and Grail's stringified longs —
	// a string-serialized count must not silently sort as 0.
	f, _ := catalog.FloatValue(rec["numberOfMatches"])
	return f
}

func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
