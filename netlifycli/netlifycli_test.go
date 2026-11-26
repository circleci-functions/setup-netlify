package netlifycli

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestBashPath(t *testing.T) {
	// filepath.ToSlash only rewrites the OS's own separator, so a Windows path
	// is only fully converted on Windows; the drive letter is handled anywhere.
	assert.Check(t, cmp.Equal(bashPath("C:/Users/circleci/.cache"), "/c/Users/circleci/.cache"))
	assert.Check(t, cmp.Equal(bashPath("/home/circleci/.cache"), "/home/circleci/.cache"))
}

func TestParseResult(t *testing.T) {
	for name, stdout := range map[string]string{
		"pretty":             "{\n  \"deploy_id\": \"dep_1\",\n  \"deploy_url\": \"https://dep_1--s.netlify.app\"\n}\n",
		"after other output": "Deploying...\n{\n  \"deploy_id\": \"dep_1\",\n  \"deploy_url\": \"https://dep_1--s.netlify.app\"\n}",
		"compact":            `{"deploy_id":"dep_1","deploy_url":"https://dep_1--s.netlify.app"}`,
	} {
		t.Run(name, func(t *testing.T) {
			r, err := ParseResult(stdout)
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(r.DeployID, "dep_1"))
			assert.Check(t, cmp.Equal(r.DeployURL, "https://dep_1--s.netlify.app"))
		})
	}
	for _, stdout := range []string{"", "Deploy is live!", `{"site_id":"s"}`} {
		_, err := ParseResult(stdout)
		assert.Check(t, cmp.ErrorContains(err, "printed no deploy ID"), stdout)
	}
}

func TestNodeVersion(t *testing.T) {
	major, minor, ok := nodeVersion("v22.13.1")
	assert.Check(t, ok)
	assert.Check(t, cmp.Equal(major, 22))
	assert.Check(t, cmp.Equal(minor, 13))
	_, _, ok = nodeVersion("v22")
	assert.Check(t, !ok)
}
