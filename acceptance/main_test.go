//go:build acceptance

// Package acceptance_test runs setup-netlify with the real Netlify CLI it pins,
// installed from npm, against a fake Netlify API: NETLIFY_API_URL points the
// CLI at the fake, so no Netlify account is involved. Tagged out of `task test`
// because it needs Node.js and the npm registry. The CLI contract against a
// stand-in CLI is covered hermetically by clitest/.
package acceptance_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-netlify/internal/testing/fakenetlify"
)

// binaryPathEnvVar names a prebuilt setup-netlify to test, for an executor with
// no Go toolchain to compile one with. The smoke jobs set it, and pair it with a
// cross-compiled copy of this suite. A relative path is resolved here, since
// the suite runs the binary from a working directory of its own.
const binaryPathEnvVar = "SETUP_NETLIFY_BINARY"

var binaryPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	if bin := os.Getenv(binaryPathEnvVar); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		binaryPath = abs
		return m.Run(), nil
	}

	p := compiler.NewParallel(1)
	defer p.Cleanup()

	p.Add(compiler.Work{
		Result: &binaryPath,
		Name:   "setup-netlify",
		Target: "..",
		Source: ".",
	})

	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// env is a test's sandbox, fake Netlify API, site directory and the variables
// a job would set.
type env struct {
	sandbox    *sdktest.Sandbox
	netlify    *fakenetlify.Server
	projectDir string
	vars       map[string]string
}

// netlifyTOML is the fixture site's config. Its build command prints the
// deploy context the build runs in, through node so it reads the same on Windows.
const netlifyTOML = `[build]
  publish = "public"
  command = "node -e \"console.log('built for ' + process.env.CONTEXT)\""
`

// newEnv returns an env with a one-page static site to deploy. The CLI cache is
// CircleCI's own when TOOLKIT_CACHE_MODE=live, as in CI, and in-memory otherwise.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.MkdirAll(filepath.Join(dir, "public"), 0o750))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "public", "index.html"), []byte("<h1>hello</h1>\n"), 0o600))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "netlify.toml"),
		[]byte(netlifyTOML), 0o600))
	e := &env{
		sandbox:    sdktest.NewSandbox(t, sdktest.WithLiveCache()),
		netlify:    fakenetlify.New(t, "site_1"),
		projectDir: dir,
		vars: map[string]string{
			"NETLIFY_AUTH_TOKEN": fakenetlify.Token,
			"NETLIFY_SITE_ID":    "site_1",
			"CIRCLE_SHA1":        "abc123",
		},
	}
	t.Cleanup(func() {
		assert.Check(t, len(e.netlify.Unhandled()) == 0, "the CLI called routes the fake does not have: %v", e.netlify.Unhandled())
		if t.Failed() {
			t.Logf("requests: %v", e.netlify.Requests())
		}
	})
	return e
}

// run runs the binary with the fake API and the sandbox's install directory,
// returning stdout and stderr together.
func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append(args,
		"--api-url="+e.netlify.URL,
		"--poll-interval=10ms",
	)
	vars := e.sandbox.Env()
	maps.Copy(vars, e.vars)
	r := runner.New(t, binaryPath, runner.WithSandboxEnv(vars))
	_, err := r.Run(full...)
	return r.StdoutString() + r.StderrString(), err
}

// cliFlags are the flags the commands that run the Netlify CLI take, keeping
// its install inside the sandbox.
func (e *env) cliFlags() []string {
	return []string{
		"--install-dir=" + e.sandbox.InstallDir,
		"--cache-nonce=" + e.sandbox.CacheKeyNonce,
	}
}

func (e *env) exported(t *testing.T) map[string]string {
	t.Helper()
	vars, err := bashenv.Vars(e.sandbox.BashEnv, map[string]string{})
	assert.NilError(t, err)
	return vars
}
