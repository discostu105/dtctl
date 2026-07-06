package catalog

import (
	"strings"
	"testing"
)

func TestDetailQuery(t *testing.T) {
	got := DetailQuery(Entity{ID: "HOST-AAAABBBBCCCCDDDD", Type: "HOST"})
	want := "smartscapeNodes \"HOST\"\n| filter id == toSmartscapeId(\"HOST-AAAABBBBCCCCDDDD\")\n| fieldsAdd references\n| limit 1"
	if got != want {
		t.Errorf("DetailQuery:\ngot  %q\nwant %q", got, want)
	}
}

func factValues(t *testing.T, entityType string, rec map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range KeyFacts(entityType) {
		out[f.Label] = f.Value(rec)
	}
	return out
}

func TestKeyFactsHost(t *testing.T) {
	rec := map[string]any{
		"id":                    "HOST-AAAABBBBCCCCDDDD",
		"name":                  "web-01.example.invalid",
		"os.type":               "OS_TYPE_LINUX",
		"os.version":            "Test Linux 1.0",
		"logical_cores":         "2",
		"cores":                 "1",
		"memory":                "8198213632",
		"ip":                    []any{"10.0.0.1"},
		"cloud.provider":        "aws",
		"aws.availability_zone": "us-east-1b",
		"sku":                   "t3.large",
		"aws.resource.id":       "i-00000000000000000",
		"dt.host_group.id":      "group-a",
		"lifetime":              map[string]any{"start": "2026-06-18T15:00:00.000000000Z", "end": "2026-07-05T20:51:00.000000000Z"},
	}
	facts := factValues(t, "HOST", rec)
	for label, want := range map[string]string{
		"id":         "HOST-AAAABBBBCCCCDDDD",
		"os":         "LINUX · Test Linux 1.0",
		"cpu":        "2 logical / 1 physical",
		"memory":     "7.6 GiB",
		"ip":         "10.0.0.1",
		"cloud":      "aws us-east-1b",
		"instance":   "t3.large (i-00000000000000000)",
		"host group": "group-a",
	} {
		if facts[label] != want {
			t.Errorf("fact %q = %q, want %q", label, facts[label], want)
		}
	}
	if facts["first seen"] == "" || facts["last seen"] == "" {
		t.Errorf("lifetime facts empty: %+v", facts)
	}
}

func TestKeyFactsSparseRecordSkipsEmpty(t *testing.T) {
	facts := factValues(t, "HOST", map[string]any{"id": "HOST-1"})
	if facts["cloud"] != "" || facts["instance"] != "" || facts["first seen"] != "" {
		t.Errorf("facts on sparse record should be empty: %+v", facts)
	}
}

func TestKeyFactsFallbackType(t *testing.T) {
	facts := factValues(t, "DISK", map[string]any{"id": "DISK-1", "type": "DISK"})
	if facts["id"] != "DISK-1" || facts["type"] != "DISK" {
		t.Errorf("fallback facts = %+v", facts)
	}
}

func TestKeyFactsPod(t *testing.T) {
	facts := factValues(t, "K8S_POD", map[string]any{
		"id":                "K8S_POD-1",
		"k8s.pod.phase":     "Running",
		"k8s.workload.kind": "deployment",
		"k8s.workload.name": "checkout",
	})
	if facts["phase"] != "Running" || facts["workload"] != "deployment checkout" {
		t.Errorf("pod facts = %+v", facts)
	}
}

func TestKeyFactsService(t *testing.T) {
	labels := strings.Builder{}
	for _, f := range KeyFacts("SERVICE") {
		labels.WriteString(f.Label + ",")
	}
	for _, want := range []string{"id", "detection", "first seen", "last seen"} {
		if !strings.Contains(labels.String(), want) {
			t.Errorf("SERVICE facts missing %q (have %s)", want, labels.String())
		}
	}
}
