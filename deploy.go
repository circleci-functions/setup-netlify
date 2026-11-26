package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/handoff"
	"github.com/circleci-functions/setup-netlify/netlify"
	"github.com/circleci-functions/setup-netlify/netlifycli"
)

type deployCmd struct {
	installFlags `embed:""`
	apiFlags     `embed:""`

	Environment string `enum:"preview,production" default:"preview" help:"Environment to deploy to: preview or production. It picks the environment variables the build reads, and whether production serves the deploy." example:"production"`
	Staged      bool   `help:"With environment production, build with production's environment variables but leave production serving what it serves now, so the deploy can be tested and then promoted."`
	Build       bool   `default:"true" negatable:"" help:"Run the site's build command in the job before deploying. Set to false to upload the publish directory as it is."`
	Dir         string `help:"Directory to publish. Empty uses the publish directory in netlify.toml." example:"dist"`
	Functions   string `help:"Directory of Netlify Functions to deploy. Empty uses the functions directory in netlify.toml." example:"netlify/functions"`
	Alias       string `help:"With environment preview, publish at a predictable address, https://<alias>--<site>.netlify.app. At most 37 characters." example:"pr-${CIRCLE_PR_NUMBER}"`
	Message     string `help:"Title for the deploy in the Netlify app."`
	PassEnv     string `name:"pass-env" default:"DATABASE_URL" help:"Comma- or newline-separated names of job environment variables to hand to the deploy's functions, as secrets. Unset ones are skipped." example:"DATABASE_URL,REDIS_URL"`
	ProjectDir  string `name:"project-dir" default:"." help:"Directory holding netlify.toml." example:"apps/web"`
	Timeout     string `default:"10m" help:"How long to wait for the deploy to be ready."`
}

func (c *deployCmd) Run(ctx context.Context, client *sdk.Client) error {
	production := c.Environment == "production"
	if c.Staged && !production {
		return errors.New("--staged needs --environment=production: a preview never takes production anyway")
	}
	if c.Alias != "" && production {
		return errors.New("--alias is for previews: production is served at the site's own address")
	}
	timeout, err := parseDuration("timeout", c.Timeout)
	if err != nil {
		return err
	}
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	cli, err := c.install(ctx, client, s, c.APIURL)
	if err != nil {
		return err
	}

	if c.Build {
		if _, err := cli.Run(ctx, c.ProjectDir, false, "build", "--context", c.buildContext()); err != nil {
			return err
		}
	}
	out, err := cli.Run(ctx, c.ProjectDir, true, c.deployArgs(timeout.Seconds())...)
	if err != nil {
		return err
	}
	res, err := netlifycli.ParseResult(out)
	if err != nil {
		return err
	}
	d, err := s.waiter(timeout).Ready(ctx, res.DeployID)
	if err != nil {
		return err
	}
	return handoff.Publish(ctx, client, "deploy", d, res.DeployURL, production && !c.Staged, os.Stderr)
}

// buildContext is the deploy context whose environment variables the build reads.
func (c *deployCmd) buildContext() string {
	if c.Environment == "production" {
		return netlify.ContextProduction
	}
	return "deploy-preview"
}

func (c *deployCmd) deployArgs(timeoutSeconds float64) []string {
	// No --context: the CLI only takes it when it builds, and the build has
	// run by now.
	args := []string{"deploy", "--no-build", "--json",
		"--message", deployTitle(c.Message, c.kind(), os.Getenv("CIRCLE_SHA1")),
		"--timeout", strconv.Itoa(int(math.Ceil(timeoutSeconds))),
	}
	if c.Dir != "" {
		args = append(args, "--dir", c.Dir)
	}
	if c.Functions != "" {
		args = append(args, "--functions", c.Functions)
	}
	if c.Environment == "production" && !c.Staged {
		args = append(args, "--prod")
	}
	if c.Alias != "" {
		args = append(args, "--alias", c.Alias)
	}
	for _, name := range splitList(c.PassEnv) {
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			fmt.Fprintf(os.Stderr, "setup-netlify: %s is not set, so the deploy's functions do not get it\n", name)
			continue
		}
		args = append(args, "--secret-env", name+"="+v)
	}
	return args
}

func (c *deployCmd) kind() string {
	switch {
	case c.Staged:
		return kindStaged
	case c.Environment == "production":
		return kindProduction
	}
	return kindPreview
}

// What a deploy's title records about it, so promote can find a staged deploy
// from a later job. Netlify keeps no commit for a deploy the CLI makes.
const (
	kindPreview    = "preview"
	kindProduction = "production"
	kindStaged     = "staged"
	titleTag       = "[setup-netlify "
)

func deployTitle(message, kind, sha string) string {
	tag := titleTag + kind
	if sha != "" {
		tag += " " + sha
	}
	return strings.TrimSpace(message + " " + tag + "]")
}

// isStaged reports whether d is a staged deploy, of sha when sha is set.
func isStaged(d netlify.Deploy, sha string) bool {
	tag := titleTag + kindStaged
	if sha != "" {
		return strings.Contains(d.Title, tag+" "+sha+"]")
	}
	return strings.Contains(d.Title, tag+" ") || strings.Contains(d.Title, tag+"]")
}
