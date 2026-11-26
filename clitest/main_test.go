// Package clitest_test exercises the setup-netlify CLI contract end to end
// against fakes: a fake Netlify API (internal/testing/fakenetlify), a fake
// CircleCI plugin server (sdktest's sandbox) and, for the commands that run the
// Netlify CLI, a fake netlify and node. Hermetic and no network, so it runs
// under `task test` on every commit.
package clitest_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"testing"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"

	"github.com/circleci-functions/setup-netlify/internal/testing/fakenetlify"
)

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

// env is a test's sandbox, fake Netlify API and the variables a job would set.
type env struct {
	sandbox *sdktest.Sandbox
	netlify *fakenetlify.Server
	vars    map[string]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return &env{
		sandbox: sdktest.NewSandbox(t),
		netlify: fakenetlify.New(t, "site_1"),
		vars: map[string]string{
			"NETLIFY_AUTH_TOKEN": fakenetlify.Token,
			"NETLIFY_SITE_ID":    "site_1",
			"CIRCLE_SHA1":        "abc123",
		},
	}
}

// run runs the binary with the test hooks pointing at the fakes. It returns
// stdout and stderr together.
func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	full := append(args, "--api-url="+e.netlify.URL, "--poll-interval=1ms")
	vars := e.sandbox.Env()
	maps.Copy(vars, e.vars)
	r := runner.New(t, binaryPath, runner.WithSandboxEnv(vars))
	_, err := r.Run(full...)
	return r.StdoutString() + r.StderrString(), err
}

// exported returns the variables the run exported to later steps.
func (e *env) exported(t *testing.T) map[string]string {
	t.Helper()
	vars, err := bashenv.Vars(e.sandbox.BashEnv, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	return vars
}
