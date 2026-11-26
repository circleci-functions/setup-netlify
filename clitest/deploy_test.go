package clitest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-netlify/internal/testing/fakenetlify"
	"github.com/circleci-functions/setup-netlify/netlifycli"
)

// fakeCLI puts a stand-in netlify where setup-netlify installs it, and a node
// on PATH, so deploy and setup run without npm or the network. The stand-in
// logs each invocation's arguments, and for deploy prints the JSON the real one
// does, for deployID.
func fakeCLI(t *testing.T, e *env, deployID, nodeVersion string) (installDir, argLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in CLI is a shell script")
	}
	installDir = t.TempDir()
	argLog = filepath.Join(t.TempDir(), "args")
	bin := netlifycli.Bin(filepath.Join(installDir, netlifycli.DefaultVersion))
	writeScript(t, bin, `echo "$*" >> `+argLog+`
echo "token=${NETLIFY_AUTH_TOKEN:+set} site=$NETLIFY_SITE_ID" >> `+argLog+`
case "$1" in
  --version) echo "netlify-cli/`+netlifycli.DefaultVersion+` linux-x64 node-v22.20.0" ;;
  build) echo "build output" ;;
  deploy) echo '{
  "site_id": "site_1",
  "site_name": "my-site",
  "deploy_id": "`+deployID+`",
  "deploy_url": "https://`+deployID+`--my-site.netlify.app",
  "logs": "https://app.netlify.com/projects/my-site/deploys/`+deployID+`"
}' ;;
esac
`)

	nodeDir := t.TempDir()
	writeScript(t, filepath.Join(nodeDir, "node"), `echo `+nodeVersion)
	e.vars["PATH"] = nodeDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return installDir, argLog
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	assert.NilError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755)) //nolint:gosec // an executable
}

func invocations(t *testing.T, argLog string) []string {
	t.Helper()
	b, err := os.ReadFile(argLog)
	assert.NilError(t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestDeploy_PreviewBuildsInTheJob(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_1", Context: "deploy-preview", States: []string{"processing", "ready"}})
	installDir, argLog := fakeCLI(t, e, "dep_1", "v22.20.0")

	out, err := e.run(t, "deploy", "--install-dir="+installDir)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{
		"build --context deploy-preview",
		"token=set site=site_1",
		"deploy --no-build --json --message [setup-netlify preview abc123] --timeout 600",
		"token=set site=site_1",
	}))
	assert.Check(t, cmp.Contains(out, "build output"))
	assert.Check(t, cmp.Contains(out, "DATABASE_URL is not set"))
	assert.Check(t, cmp.Equal(e.exported(t)["DEPLOYMENT_URL"], "https://dep_1--my-site.netlify.app"))
	assert.Check(t, !strings.Contains(out, fakenetlify.Token), "the token was printed:\n%s", out)
}

func TestDeploy_StagedProduction(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_1", Context: "deploy-preview"})
	installDir, argLog := fakeCLI(t, e, "dep_1", "v22.20.0")
	e.vars["DATABASE_URL"] = "postgres://db.example/app"

	out, err := e.run(t, "deploy", "--install-dir="+installDir,
		"--environment=production", "--staged", "--message=release", "--dir=dist", "--functions=fns", "--timeout=90s")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{
		"build --context production",
		"token=set site=site_1",
		"deploy --no-build --json --message release [setup-netlify staged abc123] --timeout 90 --dir dist --functions fns --secret-env DATABASE_URL=postgres://db.example/app",
		"token=set site=site_1",
	}))
	assert.Check(t, !strings.Contains(out, "postgres://"), "the secret was printed:\n%s", out)
}

func TestDeploy_ProductionWithoutBuild(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_1", Context: "production"})
	installDir, argLog := fakeCLI(t, e, "dep_1", "v22.20.0")

	out, err := e.run(t, "deploy", "--install-dir="+installDir, "--environment=production", "--no-build", "--pass-env=")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(invocations(t, argLog), []string{
		"deploy --no-build --json --message [setup-netlify production abc123] --timeout 600 --prod",
		"token=set site=site_1",
	}))
}

func TestDeploy_FlagConflicts(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--staged"}, "--staged needs --environment=production"},
		{[]string{"--environment=production", "--alias=x"}, "--alias is for previews"},
		{[]string{"--timeout=soon"}, `--timeout="soon": want a positive duration`},
	} {
		out, err := e.run(t, append([]string{"deploy"}, tc.args...)...)
		assert.Check(t, err != nil, out)
		assert.Check(t, cmp.Contains(out, tc.want))
	}
	assert.Check(t, cmp.Len(e.netlify.Requests(), 0))
}

func TestDeploy_OldNode(t *testing.T) {
	e := newEnv(t)
	installDir, _ := fakeCLI(t, e, "dep_1", "v22.12.0")

	out, err := e.run(t, "deploy", "--install-dir="+installDir)
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "needs Node.js 22.13 or later, and node on PATH is v22.12.0"))
}

func TestSetup_LinksTheCLI(t *testing.T) {
	e := newEnv(t)
	installDir, _ := fakeCLI(t, e, "", "v24.1.0")
	binDir := t.TempDir()

	out, err := e.run(t, "--install-dir="+installDir, "--bin-dir="+binDir)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Contains(out, "netlify "+netlifycli.DefaultVersion+" resolves from "+binDir))
	_, err = os.Lstat(filepath.Join(binDir, "netlify"))
	assert.Check(t, err)
}
