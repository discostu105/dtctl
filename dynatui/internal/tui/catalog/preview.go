package catalog

import (
	"fmt"
	"strings"
	"time"
)

// The table preview pane's per-record-kind treatment. PriorityFields (the
// inspector's hoist list) names raw record fields; the preview pane instead
// tells the triage story of ONE record in a handful of formatted lines —
// durations humanized, timestamps as clock times, verdicts colored — with the
// kind-specific substance (a DB span's statement, a session's activity, a
// view's web vitals) ordered so a cramped bottom panel truncates the boring
// tail, not the point.

// PreviewFact is one rendered line of the preview pane: a short lowercase
// label, a formatted value, an optional semantic render class ("error",
// "warn", "ok", "dim"), and Wrap for long prose that should flow over a few
// lines instead of truncating.
type PreviewFact struct {
	Label string
	Value string
	Class string
	Wrap  bool
}

// previewBuilder collects facts, dropping empty values so branches can probe
// fields only some records carry.
type previewBuilder struct{ facts []PreviewFact }

func (b *previewBuilder) add(label, value string) { b.classed(label, value, "") }
func (b *previewBuilder) wrapped(label, value string) {
	if value != "" {
		b.facts = append(b.facts, PreviewFact{Label: label, Value: value, Wrap: true})
	}
}
func (b *previewBuilder) classed(label, value, class string) {
	if value != "" {
		b.facts = append(b.facts, PreviewFact{Label: label, Value: value, Class: class})
	}
}

// PreviewTitle returns the headline for a record's preview pane — the most
// specific identity its kind offers ("" lets the caller probe generic keys).
func PreviewTitle(rec map[string]any) string {
	switch {
	case Str(rec, "event.kind") == "DAVIS_PROBLEM":
		return joinNonEmpty(" ", Str(rec, "display_id"), Str(rec, "event.name"))
	case isSpanRecord(rec):
		return spanLabel(rec)
	case isSessionRecord(rec):
		if app := StrFirst(rec, "frontend.name"); app != "" {
			return app + " session"
		}
		return "user session"
	case rec["characteristics.classifier"] != nil:
		// A view summary's identity is the VIEW — userEventDetail would title
		// every SPA route by its url.path ("/"), validated live.
		if Str(rec, "characteristics.classifier") == "view_summary" {
			if v := firstNonEmpty(Str(rec, "view.name"), Str(rec, "view.detected_name")); v != "" {
				return v
			}
		}
		return userEventDetail(rec)
	case isSyntheticRecord(rec):
		return Str(rec, "monitor.name")
	case isAttackRecord(rec):
		return Str(rec, "finding.title")
	case isBizEventRecord(rec):
		return firstNonEmpty(Str(rec, "event.name"), Str(rec, "event.type"))
	}
	return ""
}

// isAttackRecord matches security detection findings (Runtime Application
// Protection attacks and other DETECTION_FINDING producers).
func isAttackRecord(rec map[string]any) bool {
	return Str(rec, "finding.id") != "" || Str(rec, "finding.title") != ""
}

// PreviewFacts returns the curated preview lines for a record, nil for kinds
// without a curated treatment (the caller falls back to the generic
// priority-field rendering). Discriminators mirror PriorityFields.
func PreviewFacts(rec map[string]any) []PreviewFact {
	switch {
	case Str(rec, "event.kind") == "DAVIS_PROBLEM":
		return problemPreview(rec)
	case Str(rec, "event.kind") == "DAVIS_EVENT":
		return davisEventPreview(rec)
	case isAttackRecord(rec):
		// Before the vulnerability arm: attack records carry
		// vulnerability.code_location.name but are detections, not vulns.
		return attackPreview(rec)
	case rec["vulnerability.id"] != nil || Str(rec, "vulnerability.display_id") != "":
		return vulnPreview(rec)
	case isSpanRecord(rec):
		return spanPreview(rec)
	case isSessionRecord(rec):
		return sessionPreview(rec)
	case rec["characteristics.classifier"] != nil:
		return rumEventPreview(rec)
	case isSyntheticRecord(rec):
		return syntheticPreview(rec)
	case isBizEventRecord(rec):
		return bizEventPreview(rec)
	case Str(rec, "content") != "":
		return logPreview(rec)
	}
	return nil
}

// PreviewValue formats one record field for the preview pane's generic
// fallback path: known time fields render as clock times, durations
// humanize, everything else goes through FormatValue.
func PreviewValue(key string, val any) string {
	switch key {
	case "timestamp", "start_time", "event.start", "event.end":
		return FormatTime(FormatValue(val))
	case "duration":
		return FormatNs(val)
	}
	return FormatValue(val)
}

func isSpanRecord(rec map[string]any) bool {
	return rec["span.kind"] != nil || Str(rec, "span.name") != ""
}

// isSessionRecord matches user.sessions rows (the aggregate record, not the
// per-event rows — those carry characteristics.classifier).
func isSessionRecord(rec map[string]any) bool {
	return rec["user_action_count"] != nil || Str(rec, "end_reason") != ""
}

func isSyntheticRecord(rec map[string]any) bool {
	return Str(rec, "monitor.name") != "" || rec["result.state"] != nil
}

// isBizEventRecord matches BIZ_EVENT records plus producers that omit the
// kind but stamp a provider (validated live, same arm as PriorityFields).
func isBizEventRecord(rec map[string]any) bool {
	return Str(rec, "event.kind") == "BIZ_EVENT" ||
		(Str(rec, "event.provider") != "" && Str(rec, "event.kind") == "")
}

// spanPreview: verdict and latency first (the triage question), then the
// category substance — the statement, prompt, destination, or call this span
// is actually about — then where it happened in code and infrastructure.
// Field names validated live and against the semantic dictionary's span
// models (span/http/db/messaging/rpc/faas).
func spanPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	if s := spanStatus(rec); s != "" {
		b.classed("status", s, classSpanStatus(s))
	}
	if exited, _ := rec["span.is_exit_by_exception"].(bool); exited {
		b.classed("exception", "span exited by exception", "error")
	}
	b.wrapped("status message", Str(rec, "span.status_message"))
	b.add("duration", FormatNs(rec["duration"]))
	switch {
	case GenAIOp(rec) != "":
		op := GenAIOp(rec)
		b.add("op", GenAIOpShort(op))
		b.add("model", GenAIModel(rec))
		b.add("tokens", GenAITokens(rec))
		label := "prompt"
		switch op {
		case "execute_tool":
			label = "tool"
		case "invoke_agent":
			label = "agent"
		}
		b.wrapped(label, GenAIDetail(rec))
	case SpanCategory(rec) == "db":
		b.add("db", joinNonEmpty(" · ",
			firstNonEmpty(Str(rec, "db.system.name"), Str(rec, "db.system")),
			firstNonEmpty(Str(rec, "db.namespace"), Str(rec, "db.name"))))
		b.add("peer", spanPeer(rec))
		b.wrapped("statement", firstNonEmpty(Str(rec, "db.query.text"), Str(rec, "db.statement")))
	case SpanCategory(rec) == "messaging":
		b.add("destination", Str(rec, "messaging.destination.name"))
		b.add("op", joinNonEmpty(" · ",
			firstNonEmpty(Str(rec, "messaging.operation.type"), Str(rec, "messaging.operation")),
			Str(rec, "messaging.system")))
		b.add("kafka", spanKafka(rec))
		b.add("peer", spanPeer(rec))
	case Str(rec, "rpc.service") != "" || Str(rec, "rpc.system") != "":
		b.add("call", joinNonEmpty(".", Str(rec, "rpc.service"), Str(rec, "rpc.method")))
		b.add("rpc", Str(rec, "rpc.system"))
		b.add("peer", spanPeer(rec))
	case Str(rec, "faas.name") != "":
		b.add("faas", Str(rec, "faas.name"))
		trigger := Str(rec, "faas.trigger")
		if cold, _ := rec["faas.coldstart"].(bool); cold {
			trigger = joinNonEmpty(" · ", trigger, "cold start")
		}
		b.add("trigger", trigger)
	default:
		// Both semconv eras: http.request.method/http.response.status_code
		// (stable OTel) next to http.method/http.status_code (OneAgent).
		if method := firstNonEmpty(Str(rec, "http.request.method"), Str(rec, "http.method")); method != "" {
			status := firstNonEmpty(FormatValue(rec["http.response.status_code"]), FormatValue(rec["http.status_code"]))
			b.classed("http", strings.TrimSpace(method+" "+status), classHTTPStatus(status))
			b.add("url", firstNonEmpty(Str(rec, "url.full"), Str(rec, "http.url"), Str(rec, "url.path"), Str(rec, "http.target")))
			if Str(rec, "span.kind") == "server" {
				b.add("client", Str(rec, "client.ip"))
			} else {
				b.add("peer", spanPeer(rec))
			}
		}
	}
	// The code location and placement OneAgent stamps on nearly every span.
	b.add("code", joinNonEmpty(".", Str(rec, "code.namespace"), Str(rec, "code.function")))
	b.add("service", SpanService(rec))
	b.add("host", Str(rec, "host.name"))
	b.add("release", joinNonEmpty(" · ",
		Str(rec, "deployment.release_build_version"),
		Str(rec, "deployment.release_stage")))
	b.add("kind", Str(rec, "span.kind"))
	b.add("start", FormatTime(Str(rec, "start_time")))
	return b.facts
}

// spanPeer names the remote endpoint an exit span talked to ("db-host:5432").
func spanPeer(rec map[string]any) string {
	addr := Str(rec, "server.address")
	if addr == "" {
		return ""
	}
	if port := FormatValue(rec["server.port"]); port != "" {
		return addr + ":" + port
	}
	return addr
}

// spanKafka gathers the Kafka specifics worth a glance ("partition 3 ·
// offset 1284 · group billing").
func spanKafka(rec map[string]any) string {
	var parts []string
	if p := FormatValue(rec["messaging.destination.partition.id"]); p != "" {
		parts = append(parts, "partition "+p)
	}
	if o := FormatValue(rec["messaging.kafka.offset"]); o != "" {
		parts = append(parts, "offset "+o)
	}
	if g := Str(rec, "messaging.consumer.group.name"); g != "" {
		parts = append(parts, "group "+g)
	}
	return strings.Join(parts, " · ")
}

// sessionPreview: how long, how much happened, did it hurt, who/where from.
func sessionPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	b.add("duration", FormatNs(rec["duration"]))
	b.add("activity", sessionActivity(rec))
	if errs, ok := FloatValue(rec["error.count"]); ok && errs > 0 {
		b.classed("errors", fmt.Sprintf("%.0f", errs), "error")
	}
	b.add("client", joinNonEmpty(" · ",
		strings.TrimSpace(Str(rec, "browser.name")+" "+Str(rec, "browser.version")),
		Str(rec, "os.name"),
		Str(rec, "geo.country.iso_code")))
	b.add("start", FormatTime(Str(rec, "start_time")))
	end := Str(rec, "end_reason")
	if bounced, _ := rec["characteristics.is_bounce"].(bool); bounced {
		end = joinNonEmpty(" · ", end, "bounced")
	}
	b.add("end", end)
	return b.facts
}

// sessionActivity summarizes a session's event counts on one line
// ("4 views · 12 actions · 87 requests").
func sessionActivity(rec map[string]any) string {
	var parts []string
	for _, c := range []struct{ key, noun string }{
		{"view_summary_count", "views"},
		{"user_action_count", "actions"},
		{"request_count", "requests"},
	} {
		if f, ok := FloatValue(rec[c.key]); ok {
			parts = append(parts, fmt.Sprintf("%.0f %s", f, c.noun))
		}
	}
	return strings.Join(parts, " · ")
}

// rumEventPreview: the classifier decides what the record is about — an
// error's origin, an action's shape, a view's web vitals, a request's verdict.
func rumEventPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	classifier := Str(rec, "characteristics.classifier")
	class := ""
	if classifier == "error" {
		class = "error"
	}
	b.classed("kind", classifier, class)
	switch classifier {
	case "error":
		b.wrapped("error", firstNonEmpty(Str(rec, "error.display_name"), Str(rec, "error.name")))
		b.add("source", joinNonEmpty(" · ", Str(rec, "error.source"), Str(rec, "error.type")))
	case "user_action":
		b.add("type", joinNonEmpty(" · ", Str(rec, "user_action.type"), Str(rec, "interaction.type")))
		b.add("requests", FormatValue(rec["user_action.requests.count"]))
	case "view_summary":
		if lcp := vitalsMs(rec, "web_vitals.largest_contentful_paint"); lcp != "" {
			b.classed("LCP", lcp, classLCP(lcp))
		}
		var vitals []string
		if inp := vitalsMs(rec, "web_vitals.interaction_to_next_paint"); inp != "" {
			vitals = append(vitals, "INP "+inp)
		}
		if cls, ok := rec["web_vitals.cumulative_layout_shift"].(float64); ok {
			vitals = append(vitals, fmt.Sprintf("CLS %.3f", cls))
		}
		if ttfb := vitalsMs(rec, "web_vitals.time_to_first_byte"); ttfb != "" {
			vitals = append(vitals, "TTFB "+ttfb)
		}
		b.add("vitals", strings.Join(vitals, " · "))
		if errs := rumViewErrors(rec); errs != "" && errs != "0" {
			b.classed("errors", errs, "error")
		}
	case "request":
		status := FormatValue(rec["http.response.status_code"])
		b.classed("response", strings.TrimSpace(Str(rec, "http.request.method")+" "+status), classHTTPStatus(status))
		b.add("domain", Str(rec, "url.domain"))
	}
	if classifier != "view_summary" { // a view summary's view IS its title
		b.add("view", firstNonEmpty(Str(rec, "view.name"), Str(rec, "view.detected_name")))
	}
	b.add("app", Str(rec, "frontend.name"))
	b.add("duration", FormatNs(rec["duration"]))
	b.add("start", FormatTime(Str(rec, "start_time")))
	return b.facts
}

func problemPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	status := Str(rec, "event.status")
	b.classed("status", status, classProblemStatus(status))
	sev := SeverityBadge(Str(rec, "event.severity"))
	b.classed("severity", sev, ClassSeverityBadge(sev))
	b.add("category", Str(rec, "event.category"))
	label, value := eventLifespan(rec)
	b.add(label, value)
	b.add("affected", affectedNames(rec))
	b.wrapped("description", Str(rec, "event.description"))
	return b.facts
}

func davisEventPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	status := Str(rec, "event.status")
	b.classed("status", status, classProblemStatus(status))
	sev := SeverityBadge(Str(rec, "event.severity"))
	b.classed("severity", sev, ClassSeverityBadge(sev))
	b.add("type", Str(rec, "event.type"))
	label, value := eventLifespan(rec)
	b.add(label, value)
	b.add("entity", Str(rec, "dt_source_entity_name"))
	if relevant, _ := rec["dt.davis.is_rootcause_relevant"].(bool); relevant {
		b.classed("root cause", "relevant", "warn")
	}
	b.wrapped("description", Str(rec, "event.description"))
	return b.facts
}

// eventLifespan renders a Davis record's start→end window: closed records get
// their duration, active ones their age. The label carries the verdict.
func eventLifespan(rec map[string]any) (label, value string) {
	start, err := time.Parse(time.RFC3339Nano, Str(rec, "event.start"))
	if err != nil {
		return "", ""
	}
	startText := FormatTime(Str(rec, "event.start"))
	if end, err := time.Parse(time.RFC3339Nano, Str(rec, "event.end")); err == nil {
		return "lasted", fmt.Sprintf("%s (%s → %s)",
			FormatDuration(end.Sub(start)), startText, FormatTime(Str(rec, "event.end")))
	}
	if Str(rec, "event.status") == "ACTIVE" {
		return "active", fmt.Sprintf("%s (since %s)", Age(Str(rec, "event.start")), startText)
	}
	return "started", startText
}

// affectedNames summarizes the affected_entity_names array ("checkout,
// payments +3").
func affectedNames(rec map[string]any) string {
	names, _ := rec["affected_entity_names"].([]any)
	if len(names) == 0 {
		return ""
	}
	shown := 2
	if len(names) < shown {
		shown = len(names)
	}
	parts := make([]string, 0, shown)
	for _, n := range names[:shown] {
		parts = append(parts, FormatValue(n))
	}
	out := strings.Join(parts, ", ")
	if rest := len(names) - shown; rest > 0 {
		out += fmt.Sprintf(" +%d", rest)
	}
	return out
}

// vulnPreview coalesces the vulns view's summarize aliases with the raw
// security.events names, so both row shapes preview alike.
func vulnPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	level := firstNonEmpty(Str(rec, "level"), Str(rec, "vulnerability.risk.level"))
	score := firstNonEmpty(FormatScore(rec["score"]), FormatScore(rec["vulnerability.risk.score"]))
	class := classRiskLevel(level)
	if class == "" {
		class = classRiskScore(score)
	}
	b.classed("risk", joinNonEmpty(" · ", level, score), class)
	status := firstNonEmpty(Str(rec, "status"), Str(rec, "vulnerability.resolution.status"))
	if muted := firstNonEmpty(Str(rec, "muted"), Str(rec, "vulnerability.mute.status")); muted == "MUTED" {
		status = joinNonEmpty(" · ", status, "MUTED")
	}
	b.add("status", status)
	exposure := exposureBadge(firstNonEmpty(Str(rec, "exposure"), Str(rec, "vulnerability.davis_assessment.exposure_status")))
	b.classed("exposure", exposure, classExposure(exposure))
	if firstNonEmpty(Str(rec, "exploit"), Str(rec, "vulnerability.davis_assessment.exploit_status")) == "AVAILABLE" {
		b.classed("exploit", "publicly available", "error")
	}
	if fixAvailable(rec) {
		b.classed("fix", "available", "ok")
	}
	b.add("stack", vulnStack(firstNonEmpty(Str(rec, "stack"), Str(rec, "vulnerability.stack"))))
	b.add("affected", firstNonEmpty(FormatValue(rec["affected"]), FormatValue(rec["affected_entities.count"])))
	b.add("component", firstNonEmpty(Str(rec, "component"), Str(rec, "affected_entity.vulnerable_component.name")))
	b.add("tech", firstNonEmpty(Str(rec, "tech"), Str(rec, "vulnerability.technology")))
	b.add("cve", firstNonEmpty(StrFirst(rec, "cve"), StrFirst(rec, "vulnerability.references.cve")))
	b.classed("url", firstNonEmpty(Str(rec, "url"), Str(rec, "vulnerability.url")), "dim")
	return b.facts
}

// attackPreview tells one detection's story: was it blocked, what was
// injected, where it entered, who sent it.
func attackPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	action := Str(rec, "finding.action")
	b.classed("action", action, classAttackAction(action))
	sev := Str(rec, "finding.severity")
	b.classed("severity", sev, classRiskLevel(sev))
	b.add("type", Str(rec, "finding.type"))
	b.wrapped("payload", Str(rec, "entry_point.payload"))
	b.add("entry point", Str(rec, "entry_point.function.name"))
	b.add("path", Str(rec, "entry_point.url.path"))
	b.add("target", Str(rec, "dt.security.rap.target.name"))
	b.add("source", FormatValue(rec["actor.ips"]))
	b.add("process", attackProcess(rec))
	b.classed("trace", Str(rec, "trace.id"), "dim")
	b.add("at", FormatTime(Str(rec, "timestamp")))
	return b.facts
}

func syntheticPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	state := Str(rec, "result.state")
	b.classed("state", state, classExecutionState(state))
	b.add("event", strings.ReplaceAll(Str(rec, "event.type"), "_", " "))
	b.add("step", Str(rec, "step.name"))
	b.wrapped("message", Str(rec, "result.status.message"))
	b.add("duration", FormatNs(rec["result.statistics.duration"]))
	b.add("at", FormatTime(Str(rec, "timestamp")))
	return b.facts
}

func bizEventPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	b.add("type", Str(rec, "event.type"))
	b.add("provider", Str(rec, "event.provider"))
	b.add("category", Str(rec, "event.category"))
	b.add("at", FormatTime(Str(rec, "timestamp")))
	b.classed("id", Str(rec, "event.id"), "dim")
	return b.facts
}

// logPreview: the level verdict and the message are the point; the origin
// follows — K8s placement, the emitting process, host, and the raw source.
// Field presence validated live: pod logs carry k8s.namespace/pod/container
// names, OneAgent adds dt.process_group.detected_name and process.technology,
// and log.iostream marks stderr (the semantic dictionary's log.general set).
func logPreview(rec map[string]any) []PreviewFact {
	var b previewBuilder
	level := logLevel(rec)
	b.classed("level", level, ClassLevel(level))
	if Str(rec, "log.iostream") == "stderr" {
		b.classed("stream", "stderr", "warn")
	}
	b.wrapped("content", Str(rec, "content"))
	b.add("at", FormatTime(Str(rec, "timestamp")))
	if pod := Str(rec, "k8s.pod.name"); pod != "" {
		b.add("pod", joinNonEmpty("/", Str(rec, "k8s.namespace.name"), pod))
		b.add("container", Str(rec, "k8s.container.name"))
	} else if ns := Str(rec, "k8s.namespace.name"); ns != "" {
		// Some log forwarders stamp namespace+container without the pod.
		b.add("container", joinNonEmpty("/", ns, Str(rec, "k8s.container.name")))
	}
	b.add("process", Str(rec, "dt.process_group.detected_name"))
	b.add("host", Str(rec, "host.name"))
	b.add("tech", Str(rec, "process.technology"))
	b.add("source", Str(rec, "log.source"))
	return b.facts
}
