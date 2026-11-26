package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/handoff"
	"github.com/circleci-functions/setup-netlify/netlify"
)

type rollbackCmd struct {
	apiFlags `embed:""`

	Deployment string `help:"ID of the earlier production deploy to roll back to. Empty uses the deploy production served before the current one."`
	Timeout    string `default:"5m" help:"How long to wait for production to serve the deploy."`
}

func (c *rollbackCmd) Run(ctx context.Context, client *sdk.Client) error {
	timeout, err := parseDuration("timeout", c.Timeout)
	if err != nil {
		return err
	}
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	d, err := c.resolve(ctx, s)
	if err != nil {
		return err
	}
	if d.State != netlify.StateReady {
		return fmt.Errorf("deploy %s is %s, not ready, so production cannot serve it", d.ID, d.State)
	}
	if !d.WasPublished() {
		return fmt.Errorf("deploy %s has never served production; to publish it for the first time, use promote", d.ID)
	}

	fmt.Fprintf(os.Stderr, "setup-netlify: rolling production back to deploy %s\n", d.ID)
	if err := s.publish(ctx, d, timeout); err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "rollback", d, "", true, os.Stderr)
}

// resolve finds the deploy to roll back to: the flag, else the deploy that was
// published last before the current one.
func (c *rollbackCmd) resolve(ctx context.Context, s session) (netlify.Deploy, error) {
	if c.Deployment != "" {
		return s.deploy(ctx, c.Deployment)
	}
	current := s.site.PublishedDeploy
	if current == nil || current.ID == "" {
		return netlify.Deploy{}, errors.New("the site has no published deploy to roll back from")
	}
	ds, err := s.api.ListDeploys(ctx, s.site.ID, 100)
	if err != nil {
		return netlify.Deploy{}, err
	}
	if d, ok := previousPublished(ds, *current); ok {
		return d, nil
	}
	return netlify.Deploy{}, errors.New("found no earlier published deploy to roll back to: pass --deployment")
}

// previousPublished returns the ready deploy published most recently before
// current. Netlify moves a deploy's published_at each time it is published, so
// this is the one production served last.
func previousPublished(ds []netlify.Deploy, current netlify.Deploy) (netlify.Deploy, bool) {
	cutoff, err := time.Parse(time.RFC3339, current.PublishedAt)
	if err != nil {
		cutoff = time.Now()
	}
	var best netlify.Deploy
	var bestAt time.Time
	for _, d := range ds {
		if d.ID == current.ID || d.State != netlify.StateReady || !d.WasPublished() {
			continue
		}
		at, err := time.Parse(time.RFC3339, d.PublishedAt)
		if err != nil || !at.Before(cutoff) {
			continue
		}
		if best.ID == "" || at.After(bestAt) {
			best, bestAt = d, at
		}
	}
	return best, best.ID != ""
}
