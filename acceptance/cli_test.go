//go:build acceptance

package acceptance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-netlify/netlifycli"
)

func TestAcceptance_SetupInstallsAndLinks(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append([]string{"setup", "--bin-dir=" + e.sandbox.BinDir}, e.cliFlags()...)...)
	assert.NilError(t, err, out)

	if runtime.GOOS != "windows" {
		b, err := exec.Command(filepath.Join(e.sandbox.BinDir, "netlify"), "--version").CombinedOutput()
		assert.NilError(t, err, string(b))
		assert.Check(t, cmp.Contains(string(b), netlifycli.DefaultVersion))
	}
}

func TestAcceptance_DeployPreview(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append(e.cliFlags(), "deploy", "--project-dir="+e.projectDir)...)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Contains(out, "built for deploy-preview"))
	created := e.netlify.Created()
	assert.Assert(t, cmp.Len(created, 1))
	assert.Check(t, cmp.Equal(created[0]["draft"], true))
	assert.Check(t, strings.Contains(created[0]["title"].(string), "[setup-netlify preview abc123]"), created[0])
	assert.Check(t, cmp.Equal(e.exported(t)["DEPLOYMENT_URL"], "https://dep001--my-site.netlify.app"))
	assert.Check(t, cmp.Equal(e.netlify.Published(), ""))
}

func TestAcceptance_DeployStagedThenPromote(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, append(e.cliFlags(), "deploy", "--project-dir="+e.projectDir, "--environment=production", "--staged")...)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Contains(out, "built for production"))
	assert.Check(t, cmp.Equal(e.netlify.Published(), ""))

	delete(e.vars, "NETLIFY_DEPLOY_ID")
	out, err = e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.netlify.Published(), "dep001"))
}

func TestAcceptance_DeployProductionPassesEnvToFunctions(t *testing.T) {
	e := newEnv(t)
	e.vars["DATABASE_URL"] = "postgres://db.example/app"
	fn := filepath.Join(e.projectDir, "netlify", "functions")
	assert.NilError(t, os.MkdirAll(fn, 0o750))
	assert.NilError(t, os.WriteFile(filepath.Join(fn, "hello.mjs"),
		[]byte("export default async () => new Response(process.env.DATABASE_URL ? 'db' : 'none')\n"), 0o600))

	out, err := e.run(t, append(e.cliFlags(), "deploy", "--project-dir="+e.projectDir, "--environment=production")...)
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.netlify.Published(), "dep001"))
	updates := e.netlify.Updates()
	assert.Assert(t, cmp.Len(updates, 1))
	assert.Check(t, cmp.DeepEqual(updates[0]["environment"], []any{map[string]any{
		"key": "DATABASE_URL", "value": "postgres://db.example/app", "is_secret": true, "scopes": []any{"functions"},
	}}))
	assert.Check(t, !strings.Contains(out, "postgres://db.example/app"), "the secret was printed:\n%s", out)
}
