package main

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/setup-netlify/netlify"
)

func TestPreviousPublished(t *testing.T) {
	current := netlify.Deploy{ID: "cur", State: "ready", PublishedAt: "2026-11-26T15:10:00.000Z"}
	ds := []netlify.Deploy{
		current,
		// Republished later than it was created: published_at moves.
		{ID: "v1", State: "ready", PublishedAt: "2026-11-26T15:05:01.710Z"},
		{ID: "v2", State: "ready", PublishedAt: "2026-11-26T15:04:57.538Z"},
		{ID: "draft", State: "ready"},
		{ID: "broken", State: "error", PublishedAt: "2026-11-26T15:09:00Z"},
	}
	d, ok := previousPublished(ds, current)
	assert.Assert(t, ok)
	assert.Check(t, cmp.Equal(d.ID, "v1"))

	_, ok = previousPublished([]netlify.Deploy{current}, current)
	assert.Check(t, !ok)
}

func TestDeployTitle(t *testing.T) {
	assert.Check(t, cmp.Equal(deployTitle("", kindStaged, "abc"), "[setup-netlify staged abc]"))
	assert.Check(t, cmp.Equal(deployTitle("release 2", kindPreview, ""), "release 2 [setup-netlify preview]"))

	staged := netlify.Deploy{Title: deployTitle("x", kindStaged, "abc")}
	assert.Check(t, isStaged(staged, "abc"))
	assert.Check(t, isStaged(staged, ""))
	assert.Check(t, !isStaged(staged, "ab"))
	assert.Check(t, isStaged(netlify.Deploy{Title: deployTitle("", kindStaged, "")}, ""))
	assert.Check(t, !isStaged(netlify.Deploy{Title: deployTitle("", kindPreview, "abc")}, ""))
}
