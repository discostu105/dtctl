// Package runner embeds dtctl.wasm (GOOS=wasip1) and executes commands in
// fresh, per-request module instances: compile once, instantiate many.
//
// Each execution gets its own linear memory, environment, stdio buffers, and
// a private directory mounted as the instance's entire filesystem. HTTP is
// provided to the guest via the dtctl_host import module (see hostfns.go and
// sdk/wasihttp); the guest has no sockets and no access to the host FS beyond
// its mount.
//
// SPIKE NOTE: the per-request filesystem is a host temp directory materialized
// from Request.Files (so the generated config incl. token briefly touches
// disk). A production host would implement wazero's experimental sys.FS to
// keep the instance filesystem fully in memory.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
	"gopkg.in/yaml.v3"
)

// configFileName is where the generated per-request dtctl config lands inside
// the instance filesystem. It is excluded from Result.Files.
const configFileName = ".dtctl-service-config.yaml"

// Runner holds a compiled dtctl.wasm ready for per-request instantiation.
type Runner struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

// Request is one dtctl command execution.
type Request struct {
	// Args is the dtctl argv WITHOUT the leading "dtctl".
	Args []string
	// Files seeds the instance filesystem (name -> content). Names are
	// relative to the instance root, which is also the working directory.
	Files map[string][]byte
	// EnvironmentURL + Token become a generated single-context config.
	EnvironmentURL string
	Token          string
	// AllowedHosts restricts outbound HTTP (empty = only EnvironmentURL's
	// host). Enforced host-side in http_do.
	AllowedHosts []string
	// Timeout bounds the whole execution (default 120s).
	Timeout time.Duration
	// ExtraEnv adds/overrides guest environment variables.
	ExtraEnv map[string]string
}

// Result is what came back from the instance.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	// Files is the instance filesystem after execution (writes visible to the
	// caller; the generated config is excluded).
	Files map[string][]byte
	// MemoryBytes is the instance's linear memory size at exit.
	MemoryBytes uint64
	// Duration covers instantiate + run + teardown of the instance.
	Duration time.Duration
}

// New compiles wasmBytes and prepares the shared runtime. The returned Runner
// is safe for sequential Execute calls (one instance at a time per Runner;
// use one Runner per worker for parallelism).
func New(ctx context.Context, wasmBytes []byte) (*Runner, error) {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	if err := instantiateHostModule(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("registering dtctl_host module: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compiling dtctl.wasm: %w", err)
	}
	return &Runner{runtime: rt, compiled: compiled}, nil
}

// Close releases the runtime and compiled module.
func (r *Runner) Close(ctx context.Context) error { return r.runtime.Close(ctx) }

// Execute runs one dtctl command in a fresh instance.
func (r *Runner) Execute(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	if req.Timeout <= 0 {
		req.Timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	// Materialize the instance filesystem.
	dir, err := os.MkdirTemp("", "dtctl-wasi-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	for name, content := range req.Files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if rel, rerr := filepath.Rel(dir, p); rerr != nil || rel == ".." || len(rel) > 1 && rel[:3] == ".."+string(filepath.Separator) {
			return Result{}, fmt.Errorf("file name escapes instance root: %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(p, content, 0o600); err != nil {
			return Result{}, err
		}
	}
	cfg, err := serviceConfigYAML(req.EnvironmentURL, req.Token)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, configFileName), cfg, 0o600); err != nil {
		return Result{}, err
	}

	// Per-execution host state (HTTP handles, egress allowlist) travels via
	// context — wazero forwards this ctx into every host function call.
	st := newHostState(req.EnvironmentURL, req.AllowedHosts)
	ctx = withHostState(ctx, st)
	defer st.closeAll()

	var stdout, stderr bytes.Buffer
	// Anonymous module name so one compiled module can instantiate
	// concurrently (one instance per in-flight request).
	modCfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs(append([]string{"dtctl"}, req.Args...)...).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithFSConfig(wazero.NewFSConfig().WithDirMount(dir, "/")).
		WithSysWalltime().WithSysNanotime().WithSysNanosleep().
		WithRandSource(randSource()).
		WithEnv("HOME", "/").
		WithEnv("PWD", "/").
		WithEnv("DTCTL_CONFIG", "/"+configFileName).
		WithEnv("DTCTL_DISABLE_KEYRING", "1").
		WithEnv("NO_COLOR", "1")
	for k, v := range req.ExtraEnv {
		modCfg = modCfg.WithEnv(k, v)
	}

	res := Result{}
	mod, runErr := r.runtime.InstantiateModule(ctx, r.compiled, modCfg)
	if mod != nil {
		if mem := mod.Memory(); mem != nil {
			res.MemoryBytes = uint64(mem.Size())
		}
		_ = mod.Close(ctx)
	}
	if runErr != nil {
		var exitErr *sys.ExitError
		if errors.As(runErr, &exitErr) {
			res.ExitCode = int(exitErr.ExitCode())
		} else {
			return res, fmt.Errorf("instantiating module: %w", runErr)
		}
	}

	res.Stdout = stdout.Bytes()
	res.Stderr = stderr.Bytes()
	res.Files, err = collectFiles(dir)
	if err != nil {
		return res, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Memory reports the module's exported memory size; helper for benchmarks.
func moduleMemory(mod api.Module) uint64 {
	if mem := mod.Memory(); mem != nil {
		return uint64(mem.Size())
	}
	return 0
}

func collectFiles(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == configFileName {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	return out, err
}

// serviceConfigYAML generates the single-context config the instance sees.
// Mirrors sdk/session's Config schema (kept minimal on purpose: inline token,
// spill disabled — nothing may land outside the instance filesystem).
func serviceConfigYAML(envURL, token string) ([]byte, error) {
	if envURL == "" {
		return nil, fmt.Errorf("EnvironmentURL is required")
	}
	type namedContext struct {
		Name    string `yaml:"name"`
		Context struct {
			Environment string `yaml:"environment"`
			TokenRef    string `yaml:"token-ref"`
		} `yaml:"context"`
	}
	type namedToken struct {
		Name  string `yaml:"name"`
		Token string `yaml:"token"`
	}
	var cfg struct {
		APIVersion     string         `yaml:"apiVersion"`
		Kind           string         `yaml:"kind"`
		CurrentContext string         `yaml:"current-context"`
		Contexts       []namedContext `yaml:"contexts"`
		Tokens         []namedToken   `yaml:"tokens"`
		Spill          struct {
			Mode string `yaml:"mode"`
		} `yaml:"spill"`
	}
	cfg.APIVersion = "v1"
	cfg.Kind = "Config"
	cfg.CurrentContext = "service"
	nc := namedContext{Name: "service"}
	nc.Context.Environment = envURL
	nc.Context.TokenRef = "service-token"
	cfg.Contexts = []namedContext{nc}
	cfg.Tokens = []namedToken{{Name: "service-token", Token: token}}
	cfg.Spill.Mode = "never"
	return yaml.Marshal(cfg)
}
