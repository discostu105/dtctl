package catalog

import (
	"fmt"
	"strings"
)

// Fact is one curated line on the entity detail page's key-facts panel: the
// handful of properties someone triaging wants without reading the full
// record.
type Fact struct {
	Label string
	Value func(rec map[string]any) string
}

// DetailQuery fetches the full Smartscape node behind an entity. Validated
// live: id must be compared via toSmartscapeId() — a plain string comparison
// silently matches nothing.
func DetailQuery(e Entity) string {
	return fmt.Sprintf("smartscapeNodes %q\n| filter id == toSmartscapeId(%q)\n| fieldsRemove references\n| limit 1",
		e.Type, e.ID)
}

// KeyFacts returns the curated most-relevant properties for an entity type.
// Facts whose value is empty are skipped at render time, so a fact may probe
// fields that only some records carry.
func KeyFacts(entityType string) []Fact {
	common := []Fact{{Label: "id", Value: factField("id")}}
	switch entityType {
	case "HOST":
		return append(common,
			Fact{Label: "os", Value: hostOS},
			Fact{Label: "cpu", Value: hostCPU},
			Fact{Label: "memory", Value: func(rec map[string]any) string { return FormatBytesStr(Str(rec, "memory")) }},
			Fact{Label: "ip", Value: factField("ip")},
			Fact{Label: "cloud", Value: hostCloud},
			Fact{Label: "instance", Value: hostInstance},
			Fact{Label: "host group", Value: factField("dt.host_group.id")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "SERVICE":
		return append(common,
			Fact{Label: "detection", Value: factField("dt.service_detection.version")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	return append(common,
		Fact{Label: "type", Value: factField("type")},
		Fact{Label: "first seen", Value: lifetimeBound("start")},
		Fact{Label: "last seen", Value: lifetimeBound("end")},
	)
}

func factField(key string) func(map[string]any) string {
	return func(rec map[string]any) string { return FormatValue(rec[key]) }
}

func lifetimeBound(bound string) func(map[string]any) string {
	return func(rec map[string]any) string {
		lifetime, _ := rec["lifetime"].(map[string]any)
		if lifetime == nil {
			return ""
		}
		return FormatTime(Str(lifetime, bound))
	}
}

func hostOS(rec map[string]any) string {
	return joinNonEmpty(" · ",
		strings.TrimPrefix(Str(rec, "os.type"), "OS_TYPE_"),
		Str(rec, "os.version"))
}

func hostCPU(rec map[string]any) string {
	logical, physical := Str(rec, "logical_cores"), Str(rec, "cores")
	if logical == "" {
		return physical
	}
	out := logical + " logical"
	if physical != "" {
		out += " / " + physical + " physical"
	}
	return out
}

func hostCloud(rec map[string]any) string {
	return joinNonEmpty(" ",
		Str(rec, "cloud.provider"),
		firstNonEmpty(
			Str(rec, "aws.availability_zone"),
			Str(rec, "aws.region"),
			Str(rec, "azure.location"),
			Str(rec, "gcp.zone")))
}

func hostInstance(rec map[string]any) string {
	sku := Str(rec, "sku")
	res := firstNonEmpty(Str(rec, "aws.resource.id"), Str(rec, "azure.resource.id"))
	if sku != "" && res != "" {
		return fmt.Sprintf("%s (%s)", sku, res)
	}
	return firstNonEmpty(sku, res)
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}
