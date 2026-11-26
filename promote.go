package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/handoff"
	"github.com/circleci-functions/setup-netlify/netlify"
)

type promoteCmd struct {
	apiFlags `embed:""`

	Deployment string `help:"ID of the staged deploy to publish to production. Empty uses NETLIFY_DEPLOY_ID from an earlier step, else this commit's staged deploy."`
	Timeout    string `default:"5m" help:"How long to wait for production to serve the deploy."`
}

func (c *promoteCmd) Run(ctx context.Context, client *sdk.Client) error {
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
		return fmt.Errorf("deploy %s is %s, not ready, so it cannot be promoted", d.ID, d.State)
	}
	if d.Context != netlify.ContextProduction && !isStaged(d, "") {
		return fmt.Errorf("deploy %s is a %s built with that context's environment variables, so production would not run with its own: "+
			"deploy with environment: production and staged: true, test that deploy, then promote it", d.ID, d.Context)
	}

	if s.site.PublishedID() == d.ID {
		fmt.Fprintf(os.Stderr, "setup-netlify: production already serves deploy %s\n", d.ID)
	} else {
		fmt.Fprintf(os.Stderr, "setup-netlify: publishing deploy %s to production\n", d.ID)
		if err := s.publish(ctx, d, timeout); err != nil {
			return err
		}
	}
	return handoff.Publish(ctx, client, "promote", d, "", true, os.Stderr)
}

// resolve finds the deploy to promote: the flag, then the deploy an earlier
// step in this job exported, then this commit's staged deploy, which works
// across jobs.
func (c *promoteCmd) resolve(ctx context.Context, s session) (netlify.Deploy, error) {
	id := c.Deployment
	if id == "" {
		id = os.Getenv(handoff.EnvID)
	}
	if id != "" {
		return s.deploy(ctx, id)
	}

	sha := os.Getenv("CIRCLE_SHA1")
	if sha == "" {
		return netlify.Deploy{}, errors.New("no deploy to promote: set --deployment, or run deploy earlier in the job")
	}
	ds, err := s.api.ListDeploys(ctx, s.site.ID, 100)
	if err != nil {
		return netlify.Deploy{}, err
	}
	for _, d := range ds {
		if d.State == netlify.StateReady && isStaged(d, sha) {
			return d, nil
		}
	}
	return netlify.Deploy{}, fmt.Errorf("no staged deploy of %s to promote: deploy with environment: production and staged: true first", sha)
}
