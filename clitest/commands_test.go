package clitest_test

import (
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-netlify/internal/testing/fakenetlify"
)

func TestCredentials_MissingFailsBeforeAnyRequest(t *testing.T) {
	e := newEnv(t)
	delete(e.vars, "NETLIFY_AUTH_TOKEN")
	delete(e.vars, "NETLIFY_SITE_ID")

	out, err := e.run(t, "wait-for-preview")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "NETLIFY_AUTH_TOKEN and NETLIFY_SITE_ID are not set"))
	assert.Check(t, cmp.Len(e.netlify.Requests(), 0))
}

func TestCredentials_RejectedToken(t *testing.T) {
	e := newEnv(t)
	e.vars["NETLIFY_AUTH_TOKEN"] = "expired"

	out, err := e.run(t, "promote")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "netlify rejected NETLIFY_AUTH_TOKEN"))
	assert.Check(t, !strings.Contains(out, "expired"), "the token was printed:\n%s", out)
}

func TestCredentials_UnknownSite(t *testing.T) {
	e := newEnv(t)
	e.vars["NETLIFY_SITE_ID"] = "site_other"

	out, err := e.run(t, "rollback")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "cannot reach a site site_other"))
}

func TestWaitForPreview_ExportsTheDeploy(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_prod", Context: "production", CommitRef: "abc123"})
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_1", Context: "deploy-preview", CommitRef: "abc123", States: []string{"building", "ready"}})

	out, err := e.run(t, "wait-for-preview")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.DeepEqual(e.exported(t), map[string]string{
		"DEPLOYMENT_URL":     "https://dep_1--my-site.netlify.app",
		"NETLIFY_DEPLOY_URL": "https://dep_1--my-site.netlify.app",
		"NETLIFY_DEPLOY_ID":  "dep_1",
		"NETLIFY_ADMIN_URL":  "https://app.netlify.com/projects/my-site/deploys/dep_1",
	}))
	stored := e.sandbox.Cache.StoredArtifacts()
	assert.Assert(t, cmp.Len(stored, 1))
	assert.Check(t, cmp.Equal(stored[0].Destination, "setup-netlify/wait-for-preview.json"))
}

func TestWaitForPreview_FailedBuild(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_1", Context: "deploy-preview", CommitRef: "abc123", States: []string{"building", "error"}, ErrorMessage: "Build script returned non-zero exit code: 2"})

	out, err := e.run(t, "wait-for-preview")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "deploy dep_1 failed: Build script returned non-zero exit code: 2"))
	assert.Check(t, cmp.Contains(out, "https://app.netlify.com/projects/my-site/deploys/dep_1"))
}

func TestWaitForPreview_TimesOutWhenNothingIsBuilt(t *testing.T) {
	e := newEnv(t)
	e.netlify.SetRepoURL("https://github.com/example/site")

	out, err := e.run(t, "wait-for-preview", "--timeout=50ms")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no deploy preview of abc123 appeared"))
	assert.Check(t, cmp.Contains(out, "the site builds from Git"))
}

func TestWaitForPreview_SiteNotOnGit(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "wait-for-preview", "--timeout=50ms")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "not connected to a Git repository"))
}

func TestPromote_FindsTheStagedDeployForTheCommit(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_live", Context: "production"})
	e.netlify.SetPublished("dep_live")
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_staged", Context: "deploy-preview", Title: "[setup-netlify staged abc123]"})
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_other", Context: "deploy-preview", Title: "[setup-netlify staged fff999]"})

	out, err := e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.netlify.Published(), "dep_staged"))
	assert.Check(t, cmp.Equal(e.exported(t)["DEPLOYMENT_URL"], "https://dep_staged--my-site.netlify.app"))
}

func TestPromote_UsesTheDeployAnEarlierStepExported(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_staged", Context: "deploy-preview", Title: "release [setup-netlify staged]"})
	e.vars["NETLIFY_DEPLOY_ID"] = "dep_staged"

	out, err := e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.netlify.Published(), "dep_staged"))
}

func TestPromote_RefusesAPreview(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_pr", Context: "deploy-preview", Title: "[setup-netlify preview abc123]"})

	out, err := e.run(t, "promote", "--deployment=dep_pr")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "deploy with environment: production and staged: true"))
	assert.Check(t, cmp.Equal(e.netlify.Published(), ""))
}

func TestPromote_NoStagedDeploy(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "promote")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "no staged deploy of abc123 to promote"))
}

func TestPromote_AlreadyPublished(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_staged", Context: "deploy-preview", Title: "[setup-netlify staged abc123]"})
	e.netlify.SetPublished("dep_staged")

	out, err := e.run(t, "promote")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Contains(out, "production already serves deploy dep_staged"))
}

func TestRollback_ToThePreviouslyPublishedDeploy(t *testing.T) {
	e := newEnv(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_old", Context: "production", PublishedAt: at})
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_prev", Context: "production", PublishedAt: at.Add(time.Second)})
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_never", Context: "deploy-preview"})
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_cur", Context: "production"})
	e.netlify.SetPublished("dep_cur")

	out, err := e.run(t, "rollback")
	assert.NilError(t, err, out)
	assert.Check(t, cmp.Equal(e.netlify.Published(), "dep_prev"))
	assert.Check(t, cmp.Equal(e.exported(t)["NETLIFY_DEPLOY_ID"], "dep_prev"))
}

func TestRollback_NothingEarlier(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_cur", Context: "production"})
	e.netlify.SetPublished("dep_cur")

	out, err := e.run(t, "rollback")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "found no earlier published deploy"))
}

func TestRollback_RefusesADeployThatNeverServedProduction(t *testing.T) {
	e := newEnv(t)
	e.netlify.Add(&fakenetlify.Deploy{ID: "dep_draft", Context: "deploy-preview"})

	out, err := e.run(t, "rollback", "--deployment=dep_draft")
	assert.Check(t, err != nil, out)
	assert.Check(t, cmp.Contains(out, "has never served production"))
}
