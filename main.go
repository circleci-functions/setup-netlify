// Command setup-netlify deploys to Netlify from a CircleCI job.
//
// Supported platforms: linux, darwin, windows.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/netlifycli"
)

type cli struct {
	Setup          setupCmd          `cmd:"" default:"withargs" help:"Install the Netlify CLI and put netlify on PATH. Runs when no command is given."`
	Deploy         deployCmd         `cmd:"" help:"Build and deploy, then wait until the deploy is ready."`
	WaitForPreview waitForPreviewCmd `cmd:"" name:"wait-for-preview" help:"Wait for the deploy preview Netlify's Git integration builds for this commit."`
	Promote        promoteCmd        `cmd:"" help:"Publish a staged deploy to production."`
	Rollback       rollbackCmd       `cmd:"" help:"Publish an earlier production deploy again."`
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-netlify"),
		kong.Description("Deploy to Netlify, and promote or roll back production."),
		kong.UsageOnError(),
		kong.Vars{"netlify_version": netlifycli.DefaultVersion},
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
