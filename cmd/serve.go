package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtctl/pkg/client"
	"github.com/dynatrace-oss/dtctl/pkg/config"
	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/resources/document"
	"github.com/dynatrace-oss/dtctl/pkg/version"
	"github.com/dynatrace-oss/dtctl/pkg/webui"
	sdkquery "github.com/dynatrace-oss/dtctl/sdk/api/query"
)

// NewServeWebCommand builds `dtctl serve web`. It hangs under the
// development-tier `dtctl serve` (pkg/serve, wired in main) but, unlike the
// agent servers there, runs as an ordinary invocation on the local config.
func NewServeWebCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "web",
		Short: "Serve the dtctl web UI on localhost",
		Long: `Serve the dtctl web UI — a fast, keyboard-first, read-only observability
console for the current context — on a loopback address.

The browser never receives your token: every query is executed by this
process with the current context's credentials. The UI is read-only.`,
		Example: `  # Serve on 127.0.0.1:7878 and open the browser
  dtctl serve web

  # Serve a specific context on another port, without opening a browser
  dtctl serve web --context prod --port 9000 --no-open`,
		Args: cobra.NoArgs,
		RunE: runServeWeb,
	}
	c.Flags().Int("port", 7878, "port to listen on (the next free port is used if taken)")
	c.Flags().Bool("no-open", false, "do not open the browser")
	return c
}

func runServeWeb(cmd *cobra.Command, _ []string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	ctxObj, err := cfg.CurrentContextObj()
	if err != nil {
		return err
	}
	tenants := newWebTenants(cmd.Context(), cfg)
	if _, err := tenants.use(cfg.CurrentContext); err != nil {
		return err
	}

	srv, err := webui.New(webui.Options{
		Query: func(ctx context.Context, q, from, to string, maxRecords int64) (*sdkquery.Response, error) {
			return tenants.current().executor.ExecuteQueryWithContext(ctx, q, exec.DQLExecuteOptions{
				DefaultTimeframeStart: from,
				DefaultTimeframeEnd:   to,
				MaxResultRecords:      maxRecords,
				IncludeTypes:          true,
				ClientContext:         "dtctl-web",
				QuietCancel:           true,
			})
		},
		Documents: func(_ context.Context, docType string) ([]webui.Document, error) {
			list, err := tenants.current().docs.List(document.DocumentFilters{Type: docType, ChunkSize: 200})
			if err != nil {
				return nil, err
			}
			out := make([]webui.Document, 0, len(list.Documents))
			for _, d := range list.Documents {
				wd := webui.Document{
					ID: d.ID, Name: d.Name, Type: d.Type, Owner: d.Owner,
					Modified: d.ModificationInfo.LastModifiedTime, IsPrivate: d.IsPrivate,
				}
				if d.UserContext != nil {
					wd.LastOpened = d.UserContext.LastAccessedTime
				}
				out = append(out, wd)
			}
			return out, nil
		},
		QueryAssist: func(ctx context.Context, op string, body []byte) ([]byte, int, error) {
			resp, err := tenants.current().client.HTTP().R().
				SetContext(ctx).
				SetHeader("Content-Type", "application/json").
				SetBody(body).
				Post("/platform/storage/query/v1/query:" + op)
			if err != nil {
				return nil, 0, err
			}
			return resp.Body(), resp.StatusCode(), nil
		},
		Meta: tenants.meta,
		SwitchContext: func(name string) error {
			_, err := tenants.use(name)
			return err
		},
	})
	if err != nil {
		return err
	}

	port, _ := cmd.Flags().GetInt("port")
	noOpen, _ := cmd.Flags().GetBool("no-open")
	ln, err := listenLoopback(port)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		url = "http://localhost:" + strconv.Itoa(tcp.Port)
	}

	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(cmd.OutOrStdout(), "dtctl web — context %q (%s)\n  ➜  %s\n  press Ctrl+C to stop\n",
		cfg.CurrentContext, ctxObj.Environment, url)
	if !noOpen {
		_ = openBrowser(cmd.Context(), url)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// listenLoopback binds 127.0.0.1 on port, walking up to 20 ports forward if
// it is taken so a second `dtctl serve web` (another context) just works.
func listenLoopback(port int) (net.Listener, error) {
	var lastErr error
	for p := port; p < port+20; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no free port in %d-%d: %w", port, port+19, lastErr)
}

// webTenants holds one API client per configured context, built on first
// use, and which one the web UI currently queries. Switching only affects
// this process: the config file's current context is left alone.
type webTenants struct {
	ctx context.Context // the invocation: executors write to its streams
	cfg *config.Config

	mu   sync.Mutex
	byID map[string]*webTenant
	cur  *webTenant
}

type webTenant struct {
	name     string
	env      string
	safety   string
	client   *client.Client
	executor *exec.DQLExecutor
	docs     *document.Handler

	userOnce sync.Once
	userName string
	email    string
}

func newWebTenants(ctx context.Context, cfg *config.Config) *webTenants {
	return &webTenants{ctx: ctx, cfg: cfg, byID: map[string]*webTenant{}}
}

func (t *webTenants) use(name string) (*webTenant, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if wt, ok := t.byID[name]; ok {
		t.cur = wt
		return wt, nil
	}
	var nc *config.NamedContext
	for i := range t.cfg.Contexts {
		if t.cfg.Contexts[i].Name == name {
			nc = &t.cfg.Contexts[i]
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("context %q not found", name)
	}
	scoped := *t.cfg
	scoped.CurrentContext = name
	c, err := NewClientFromConfig(&scoped)
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	wt := &webTenant{
		name: name, env: nc.Context.Environment, safety: string(nc.Context.SafetyLevel),
		client: c, executor: newDQLExecutor(t.ctx, c), docs: document.NewHandler(c),
	}
	t.byID[name] = wt
	t.cur = wt
	return wt, nil
}

func (t *webTenants) current() *webTenant {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur
}

func (t *webTenants) meta() webui.Meta {
	wt := t.current()
	wt.userOnce.Do(func() {
		if u, err := wt.client.CurrentUser(); err == nil {
			wt.userName, wt.email = u.UserName, u.EmailAddress
		}
	})
	m := webui.Meta{
		Context: wt.name, Environment: wt.env, SafetyLevel: wt.safety, Version: version.Version,
		UserName: wt.userName, UserEmail: wt.email,
	}
	for _, nc := range t.cfg.Contexts {
		m.Contexts = append(m.Contexts, nc.Name)
		m.Tenants = append(m.Tenants, webui.Tenant{Name: nc.Name, Environment: nc.Context.Environment, SafetyLevel: string(nc.Context.SafetyLevel)})
	}
	return m
}
