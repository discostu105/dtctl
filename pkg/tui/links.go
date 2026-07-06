package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
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

// linkFor resolves the browser deep link for a selection: problems and traces
// get their native views, entities their type's app, everything else "".
func linkFor(env string, rec map[string]any, entity *catalog.Entity, traceID string) string {
	if rec != nil {
		if kind := catalog.Str(rec, "event.kind"); kind == "DAVIS_PROBLEM" && catalog.Str(rec, "event.id") != "" {
			return intentURL(env, "dynatrace.davis.problems", "view-problem",
				map[string]any{"event.id": catalog.Str(rec, "event.id"), "event.kind": kind})
		}
		if u := catalog.Str(rec, "vulnerability.url"); u != "" {
			return u
		}
	}
	if traceID != "" {
		return intentURL(env, "dynatrace.distributedtracing", "view-trace",
			map[string]any{"trace.id": traceID})
	}
	if entity != nil && entity.ID != "" {
		return entityLink(env, *entity)
	}
	return ""
}

// entityLink deep-links an entity into its type's app; unknown types land in
// the Smartscape topology explorer, which accepts any node id.
func entityLink(env string, e catalog.Entity) string {
	lower := strings.ToLower(e.Type)
	switch {
	case strings.HasPrefix(e.Type, "K8S_") || e.Type == "CONTAINER":
		return intentURL(env, "dynatrace.kubernetes", "view-entity-dt.smartscape."+lower,
			map[string]any{"nodeId": e.ID})
	case e.Type == "SERVICE":
		return intentURL(env, "dynatrace.services", "view-entity-dt.smartscape.service",
			map[string]any{"nodeId": e.ID})
	case e.Type == "DB_INSTANCE_POSTGRES":
		return intentURL(env, "dynatrace.database.overview", "view-instance-details",
			map[string]any{"dt.smartscape.db_instance_id": e.ID})
	case e.Type == "DB_DATABASE_POSTGRES":
		return intentURL(env, "dynatrace.database.overview", "view-database-details",
			map[string]any{"dt.smartscape.db_database_id": e.ID})
	default:
		return intentURL(env, "dynatrace.smartscape", "view_topology_in_context",
			map[string]any{"id": e.ID})
	}
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
