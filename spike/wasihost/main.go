// Command wasihost runs dtctl.wasm (GOOS=wasip1) in per-request wazero
// instances — the S0 spike host for the dtctl-as-a-service design.
//
// Usage:
//
//	wasihost -wasm dtctl.wasm -env-url https://tenant.example.com \
//	    [-token TOKEN] [-file name=path]... [-bench N] -- <dtctl args...>
//
// Everything after "--" is the dtctl argv. Stdout/stderr of the instance are
// forwarded; files written by the instance are listed. With -bench N the same
// command runs N times sequentially and per-instance stats are printed.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dynatrace-oss/dtctl/spike/wasihost/runner"
)

type fileFlags map[string]string

func (f fileFlags) String() string { return fmt.Sprint(map[string]string(f)) }
func (f fileFlags) Set(v string) error {
	name, path, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("-file wants name=path, got %q", v)
	}
	f[name] = path
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wasihost:", err)
		os.Exit(1)
	}
}

func run() error {
	wasmPath := flag.String("wasm", "dtctl.wasm", "path to dtctl.wasm (GOOS=wasip1 build)")
	envURL := flag.String("env-url", "", "Dynatrace environment URL for the generated context")
	token := flag.String("token", os.Getenv("WASIHOST_TOKEN"), "API token (or WASIHOST_TOKEN env)")
	bench := flag.Int("bench", 0, "run the command N times and print per-instance stats")
	timeout := flag.Duration("timeout", 120*time.Second, "per-execution timeout")
	files := fileFlags{}
	flag.Var(files, "file", "seed instance file: name=hostpath (repeatable)")
	flag.Parse()

	if *envURL == "" {
		return fmt.Errorf("-env-url is required")
	}
	args := flag.Args()
	if len(args) == 0 {
		return fmt.Errorf("no dtctl args given (pass them after --)")
	}

	wasmBytes, err := os.ReadFile(*wasmPath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	compileStart := time.Now()
	r, err := runner.New(ctx, wasmBytes)
	if err != nil {
		return err
	}
	defer r.Close(ctx)
	fmt.Fprintf(os.Stderr, "[wasihost] compiled %s (%.1f MB) in %s\n",
		*wasmPath, float64(len(wasmBytes))/1e6, time.Since(compileStart).Round(time.Millisecond))

	req := runner.Request{
		Args:           args,
		EnvironmentURL: *envURL,
		Token:          *token,
		Timeout:        *timeout,
		Files:          map[string][]byte{},
	}
	for name, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading -file %s: %w", path, err)
		}
		req.Files[name] = b
	}

	runs := 1
	if *bench > 0 {
		runs = *bench
	}
	var durations []time.Duration
	var lastExit int
	for i := 0; i < runs; i++ {
		res, err := r.Execute(ctx, req)
		if err != nil {
			return err
		}
		lastExit = res.ExitCode
		durations = append(durations, res.Duration)
		if *bench > 0 {
			fmt.Fprintf(os.Stderr, "[wasihost] run %d: exit=%d mem=%.1f MB dur=%s\n",
				i+1, res.ExitCode, float64(res.MemoryBytes)/1e6, res.Duration.Round(time.Millisecond))
			continue
		}
		os.Stdout.Write(res.Stdout)
		os.Stderr.Write(res.Stderr)
		var written []string
		for name := range res.Files {
			if _, seeded := req.Files[name]; !seeded {
				written = append(written, name)
			}
		}
		sort.Strings(written)
		fmt.Fprintf(os.Stderr, "[wasihost] exit=%d mem=%.1f MB dur=%s files-out=%v\n",
			res.ExitCode, float64(res.MemoryBytes)/1e6, res.Duration.Round(time.Millisecond), written)
	}
	if *bench > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		fmt.Fprintf(os.Stderr, "[wasihost] %d runs: min=%s p50=%s max=%s\n",
			runs, durations[0].Round(time.Millisecond),
			durations[runs/2].Round(time.Millisecond), durations[runs-1].Round(time.Millisecond))
	}
	os.Exit(lastExit)
	return nil
}
