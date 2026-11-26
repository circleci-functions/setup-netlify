package main

import (
	"context"
	"fmt"
	"os"

	"github.com/circleci-functions/toolkit/sdk"
)

type setupCmd struct {
	installFlags `embed:""`
	apiFlags     `embed:""`
}

func (c *setupCmd) Run(ctx context.Context, client *sdk.Client) error {
	s, err := c.start(ctx)
	if err != nil {
		return err
	}
	cli, err := c.install(ctx, client, s, c.APIURL)
	if err != nil {
		return err
	}
	dir, err := cli.Link(c.BinDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "setup-netlify: netlify %s resolves from %s, for site %s\n", cli.Version, dir, s.site.Name)
	return nil
}
