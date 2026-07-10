package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// Deep links use Dynatrace intent URLs ({env}/ui/intent/{app}/{intent}#{json}),
// the same mechanism as `dtctl open intent`. App/intent ids below were
// enumerated live from a tenant's intent catalog (dtctl get intents).

// intentURL builds an intent link for a payload.
func intentURL(env, app, intent string, payload map[string]any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/ui/intent/%s/%s#%s",
		strings.TrimRight(env, "/"), app, intent, url.QueryEscape(string(body)))
}

// linkOption is one "open with" target for the current selection. The first
// option of a set is the default.
type linkOption struct {
	Label string
	URL   string
}

// linkOptionsFor gathers every browser target the selection supports, most
// specific first: the record's native app (problems), URLs the record itself
// carries (vulnerability.url and friends), the trace, the entity's app, the
// Smartscape topology, and the view's query as a notebook. One option opens
// directly; several open the "open with" picker.
func linkOptionsFor(env string, rec map[string]any, entity *catalog.Entity, traceID, dql string) []linkOption {
	var opts []linkOption
	add := func(label, url string) {
		if url == "" {
			return
		}
		for _, o := range opts {
			if o.URL == url {
				return
			}
		}
		opts = append(opts, linkOption{Label: label, URL: url})
	}

	if rec != nil {
		if kind := catalog.Str(rec, "event.kind"); kind == "DAVIS_PROBLEM" && catalog.Str(rec, "event.id") != "" {
			add("problem in Davis Problems", intentURL(env, "dynatrace.davis.problems", "view-problem",
				map[string]any{"event.id": catalog.Str(rec, "event.id"), "event.kind": kind}))
		}
		// URLs the record itself carries — the vulnerability page's designated
		// link lives in vulnerability.url (aliased to plain url by the vulns
		// view's summarize), remediation and doc links elsewhere.
		for _, key := range urlFields(rec) {
			add(key, catalog.Str(rec, key))
		}
	}
	if traceID != "" {
		add("trace in Distributed Tracing", intentURL(env, "dynatrace.distributedtracing", "view-trace",
			map[string]any{"trace.id": traceID}))
	}
	if entity != nil && entity.ID != "" {
		if app, label := entityAppLink(env, *entity); app != "" {
			add(label, app)
		}
		add("topology in Smartscape", intentURL(env, "dynatrace.smartscape", "view_topology_in_context",
			map[string]any{"id": entity.ID}))
	}
	add("query in a Notebook", queryLink(env, dql))
	return opts
}

// urlMax caps how many record-carried URLs the picker offers.
const urlMax = 4

// urlFields returns the record's top-level keys holding an http(s) URL,
// alphabetically, most a picker can use.
func urlFields(rec map[string]any) []string {
	var keys []string
	for k, val := range rec {
		s, ok := val.(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > urlMax {
		keys = keys[:urlMax]
	}
	return keys
}

// entityAppLink deep-links an entity into its type's app ("" for types
// without a dedicated app — the topology option always applies).
func entityAppLink(env string, e catalog.Entity) (url, label string) {
	lower := strings.ToLower(e.Type)
	switch {
	case strings.HasPrefix(e.Type, "K8S_") || e.Type == "CONTAINER":
		return intentURL(env, "dynatrace.kubernetes", "view-entity-dt.smartscape."+lower,
			map[string]any{"nodeId": e.ID}), "entity in Kubernetes app"
	case e.Type == "SERVICE":
		return intentURL(env, "dynatrace.services", "view-entity-dt.smartscape.service",
			map[string]any{"nodeId": e.ID}), "entity in Services app"
	case e.Type == "DB_INSTANCE_POSTGRES":
		return intentURL(env, "dynatrace.database.overview", "view-instance-details",
			map[string]any{"dt.smartscape.db_instance_id": e.ID}), "instance in Databases app"
	case e.Type == "DB_DATABASE_POSTGRES":
		return intentURL(env, "dynatrace.database.overview", "view-database-details",
			map[string]any{"dt.smartscape.db_database_id": e.ID}), "database in Databases app"
	}
	return "", ""
}

// queryLink opens a DQL query in a Notebook — the browser-side counterpart
// of the reveal-query escape hatch.
func queryLink(env, dql string) string {
	if dql == "" {
		return ""
	}
	return intentURL(env, "dynatrace.notebooks", "view-query", map[string]any{"dt.query": dql})
}

// openBrowser launches the system browser (same commands as cmd/open_intent).
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// yank copies text to the system clipboard via OSC 52 (works through SSH and
// most modern terminals). The escape goes to stderr — the same tty, without
// disturbing bubbletea's stdout renderer.
func yank(text string) {
	_, _ = osc52.New(text).WriteTo(os.Stderr)
}
