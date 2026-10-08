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
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/resources/document"
	"github.com/dynatrace-oss/dtctl/pkg/version"
	"github.com/dynatrace-oss/dtctl/pkg/webui"
	sdkquery "github.com/dynatrace-oss/dtctl/sdk/api/query"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve local user interfaces",
	Long: `Serve local user interfaces backed by the current context.

Servers bind to a loopback address only and never hand credentials to the
browser.`,
	Example: `  # Serve the web UI
  dtctl serve web`,
}

var serveWebCmd = &cobra.Command{
	Use:   "web",
	Short: "[Experimental] Serve the dtctl web UI on localhost",
	Long: `Serve the dtctl web UI — a fast, keyboard-first, read-only observability
console for the current context — on a loopback address.

The browser never receives your token: every query is executed by this
process with the current context's credentials. The UI is read-only.`,
	Example: `  # Serve on 127.0.0.1:7878 and open the browser
  dtctl serve web

  # Serve a specific context on another port, without opening a browser
  dtctl serve web --context prod --port 9000 --no-open`,
	RunE: runServeWeb,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.AddCommand(serveWebCmd)
	serveWebCmd.Flags().Int("port", 7878, "port to listen on (the next free port is used if taken)")
	serveWebCmd.Flags().Bool("no-open", false, "do not open the browser")
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
	c, err := NewClientFromConfig(cfg)
	if err != nil {
		return err
	}
	executor := exec.NewDQLExecutor(c)
	docs := document.NewHandler(c)

	contexts := make([]string, 0, len(cfg.Contexts))
	for _, nc := range cfg.Contexts {
		contexts = append(contexts, nc.Name)
	}

	srv, err := webui.New(webui.Options{
		Query: func(ctx context.Context, q, from, to string, maxRecords int64) (*sdkquery.Response, error) {
			return executor.ExecuteQueryWithContext(ctx, q, exec.DQLExecuteOptions{
				DefaultTimeframeStart: from,
				DefaultTimeframeEnd:   to,
				MaxResultRecords:      maxRecords,
				IncludeTypes:          true,
				ClientContext:         "dtctl-web",
			})
		},
		Documents: func(_ context.Context, docType string) ([]webui.Document, error) {
			list, err := docs.List(document.DocumentFilters{Type: docType, ChunkSize: 200})
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
		Meta: func() webui.Meta {
			m := webui.Meta{
				Context:     cfg.CurrentContext,
				Environment: ctxObj.Environment,
				SafetyLevel: string(ctxObj.SafetyLevel),
				Version:     version.Version,
				Contexts:    contexts,
			}
			if u, err := c.CurrentUser(); err == nil {
				m.UserName, m.UserEmail = u.UserName, u.EmailAddress
			}
			return m
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
		_ = openBrowser(url)
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
