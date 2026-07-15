package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/resources/analyzer"
	"github.com/dynatrace-oss/dtctl/pkg/resources/anomalydetector"
	"github.com/dynatrace-oss/dtctl/pkg/resources/segment"
	"github.com/dynatrace-oss/dtctl/pkg/resources/slo"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// --- segment source ---------------------------------------------------------

type fakeSegmentAPI struct {
	list *segment.FilterSegmentList
	err  error
}

func (f *fakeSegmentAPI) List() (*segment.FilterSegmentList, error) { return f.list, f.err }

func TestSegmentSourceMapsAndSorts(t *testing.T) {
	src := segmentSource(&fakeSegmentAPI{list: &segment.FilterSegmentList{
		FilterSegments: []segment.FilterSegment{
			{UID: "u2", Name: "zeta"},
			{UID: "u1", Name: "Alpha", Description: "team A",
				Variables: &segment.Variables{Type: "query", Value: "fetch x"}},
			{UID: "u3", Name: "beta",
				Variables: &segment.Variables{Type: "other", Value: "ignored"}},
		},
	}})
	opts, err := src(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 3 || opts[0].Name != "Alpha" || opts[1].Name != "beta" || opts[2].Name != "zeta" {
		t.Fatalf("sort order wrong: %+v", opts)
	}
	if !opts[0].HasVariables || opts[0].VariablesQuery != "fetch x" || opts[0].Description != "team A" {
		t.Fatalf("query variables not mapped: %+v", opts[0])
	}
	if !opts[1].HasVariables || opts[1].VariablesQuery != "" {
		t.Fatalf("non-query variables must badge but carry no query: %+v", opts[1])
	}

	if _, err := segmentSource(&fakeSegmentAPI{err: errors.New("HTTP 500")})(context.Background()); err == nil {
		t.Fatal("list error must surface")
	}
}

// --- SLO source --------------------------------------------------------------

type fakeSLOAPI struct {
	list    *slo.SLOList
	listErr error
	evals   map[string]*slo.EvaluationResponse
	evalErr map[string]error
	polls   map[string][]*slo.EvaluationResponse // token → response sequence
}

func (f *fakeSLOAPI) List(string, int64) (*slo.SLOList, error) { return f.list, f.listErr }

func (f *fakeSLOAPI) Evaluate(id string) (*slo.EvaluationResponse, error) {
	if err := f.evalErr[id]; err != nil {
		return nil, err
	}
	return f.evals[id], nil
}

func (f *fakeSLOAPI) PollEvaluation(token string, _ int) (*slo.EvaluationResponse, error) {
	seq := f.polls[token]
	if len(seq) == 0 {
		return nil, errors.New("unexpected poll for " + token)
	}
	next := seq[0]
	f.polls[token] = seq[1:]
	return next, nil
}

func floatPtr(f float64) *float64 { return &f }

func TestSLOSourceMergesEvaluationsAndDegrades(t *testing.T) {
	api := &fakeSLOAPI{
		list: &slo.SLOList{SLOs: []slo.SLO{
			{ID: "slo-1", Name: "checkout availability", Tags: []string{"team:web"},
				Criteria:  []slo.Criteria{{Target: 99.5, Warning: floatPtr(99.9), TimeframeFrom: "now-7d"}},
				CustomSli: map[string]any{"indicator": "timeseries sli"}},
			{ID: "slo-2", Name: "broken"},
		}},
		evals: map[string]*slo.EvaluationResponse{
			"slo-1": {EvaluationResults: []slo.EvaluationResult{
				{Status: "SUCCESS", Value: floatPtr(99.7), ErrorBudget: floatPtr(0.2)}}},
		},
		evalErr: map[string]error{"slo-2": errors.New("evaluation exploded")},
	}
	records, err := sloSource(api)(context.Background(), catalog.Scope{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	ok := records[0]
	if ok["name"] != "checkout availability" || ok["target"] != 99.5 || ok["warning"] != 99.9 ||
		ok["window"] != "7d" || ok["indicator"] != "timeseries sli" {
		t.Fatalf("definition not flattened: %+v", ok)
	}
	if ok["status"] != "SUCCESS" || ok["sli"] != 99.7 || ok["errorBudget"] != 0.2 {
		t.Fatalf("evaluation not merged: %+v", ok)
	}
	// The failed evaluation degrades to blank cells, never an error.
	if _, has := records[1]["status"]; has {
		t.Fatalf("failed evaluation must leave the record blank: %+v", records[1])
	}
	if records[1]["name"] != "broken" {
		t.Fatalf("definition row lost: %+v", records[1])
	}
}

func TestSLOEvaluationPollsThroughTokenRotation(t *testing.T) {
	api := &fakeSLOAPI{
		evals: map[string]*slo.EvaluationResponse{
			"slo-1": {EvaluationToken: "t1"}, // long-poll: no results yet
		},
		polls: map[string][]*slo.EvaluationResponse{
			"t1": {{EvaluationToken: "t2"}}, // rotated token, still no results
			"t2": {{EvaluationResults: []slo.EvaluationResult{{Status: "FAILURE"}}}},
		},
	}
	rec := map[string]any{}
	mergeSLOEvaluation(context.Background(), api, rec, "slo-1")
	if rec["status"] != "FAILURE" {
		t.Fatalf("poll chain did not deliver the result: %+v", rec)
	}
}

// --- detector source ---------------------------------------------------------

type fakeDetectorAPI struct {
	detectors []anomalydetector.AnomalyDetector
	err       error
}

func (f *fakeDetectorAPI) List(anomalydetector.ListOptions) ([]anomalydetector.AnomalyDetector, error) {
	return f.detectors, f.err
}

func TestDetectorSourceFlattens(t *testing.T) {
	src := detectorSource(&fakeDetectorAPI{detectors: []anomalydetector.AnomalyDetector{{
		ObjectID: "obj-1", Title: "CPU saturation", Enabled: true,
		AnalyzerShort: "StaticThreshold", EventType: "RESOURCE",
		Value: map[string]any{"raw": true},
	}}})
	records, err := src(context.Background(), catalog.Scope{}, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := records[0]
	if rec["title"] != "CPU saturation" || rec["enabled"] != "true" ||
		rec["analyzer"] != "StaticThreshold" || rec["objectId"] != "obj-1" {
		t.Fatalf("detector not flattened: %+v", rec)
	}
	if _, ok := rec["value"].(map[string]any); !ok {
		t.Fatalf("raw settings value must ride along for the inspector: %+v", rec)
	}
}

// --- log-pattern source --------------------------------------------------------

type fakeAnalyzerAPI struct {
	gotName  string
	gotInput map[string]any
	result   *analyzer.ExecuteResult
	err      error
}

func (f *fakeAnalyzerAPI) ExecuteAndWait(_ context.Context, name string, input map[string]interface{}, _ int) (*analyzer.ExecuteResult, error) {
	f.gotName, f.gotInput = name, input
	return f.result, f.err
}

func TestLogPatternSourceComposesAndSorts(t *testing.T) {
	api := &fakeAnalyzerAPI{result: &analyzer.ExecuteResult{Result: &analyzer.AnalyzerResult{
		ExecutionStatus: "COMPLETED",
		Output: []map[string]any{
			{"pattern": "quiet", "numberOfMatches": float64(3)},
			{"pattern": "busy", "numberOfMatches": "1200"}, // Grail stringifies longs
			{"pattern": "middling", "numberOfMatches": float64(40)},
		},
	}}}
	scope := catalog.Scope{Timeframe: catalog.Timeframe{Label: "2h"}}
	records, err := logPatternSource(api)(context.Background(), scope, "fetch logs\n| sort timestamp desc")
	if err != nil {
		t.Fatal(err)
	}
	if api.gotName != catalog.LogPatternAnalyzer {
		t.Fatalf("analyzer = %q", api.gotName)
	}
	if q, _ := api.gotInput["logQuery"].(string); !strings.HasPrefix(q, "fetch logs") {
		t.Fatalf("composed logQuery = %q", q)
	}
	if got := []string{records[0]["pattern"].(string), records[1]["pattern"].(string), records[2]["pattern"].(string)}; got[0] != "busy" || got[1] != "middling" || got[2] != "quiet" {
		t.Fatalf("patterns not sorted by matches (string counts must not sort as 0): %v", got)
	}

	if _, err := logPatternSource(api)(context.Background(), scope, "   "); err == nil {
		t.Fatal("empty dql must refuse")
	}
	api.result = &analyzer.ExecuteResult{Result: &analyzer.AnalyzerResult{ExecutionStatus: "FAILED"}}
	if _, err := logPatternSource(api)(context.Background(), scope, "fetch logs"); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("non-completed execution must error, got %v", err)
	}
}
