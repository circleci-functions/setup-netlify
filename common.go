package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/netlify"
	"github.com/circleci-functions/setup-netlify/netlifycli"
)

// The variables setup-netlify reads its credentials from: the Netlify CLI's own.
const (
	envToken  = "NETLIFY_AUTH_TOKEN" //nolint:gosec // the variable's name, not a credential
	envSiteID = "NETLIFY_SITE_ID"
)

type credentials struct {
	Token  string
	SiteID string
}

// readCredentials fails, naming every missing variable, before any work starts.
func readCredentials() (credentials, error) {
	c := credentials{Token: os.Getenv(envToken), SiteID: os.Getenv(envSiteID)}
	var missing []string
	if c.Token == "" {
		missing = append(missing, envToken)
	}
	if c.SiteID == "" {
		missing = append(missing, envSiteID)
	}
	switch len(missing) {
	case 0:
		return c, nil
	case 1:
		return c, fmt.Errorf("%s is not set: add %s and %s to the job, for example from a context. "+
			"The site ID is the Project ID under Project configuration > General in the Netlify app", missing[0], envToken, envSiteID)
	}
	return c, fmt.Errorf("%s and %s are not set: add them to the job, for example from a context. "+
		"The site ID is the Project ID under Project configuration > General in the Netlify app", envToken, envSiteID)
}

// apiFlags are the test hooks every command takes.
type apiFlags struct {
	APIURL       string `name:"api-url" hidden:"" default:"https://api.netlify.com/api/v1" help:"Netlify API base URL."`
	PollInterval string `name:"poll-interval" hidden:"" default:"3s" help:"How often to poll Netlify while waiting."`
}

// installFlags choose the Netlify CLI the commands that need it install.
type installFlags struct {
	CLIVersion string `name:"cli-version" default:"" help:"Netlify CLI version to install. Empty installs ${netlify_version}."`
	InstallDir string `name:"install-dir" hidden:"" default:"~/.cache/setup-netlify" help:"Base directory for Netlify CLI installs."`
	BinDir     string `name:"bin-dir" hidden:"" default:"" help:"Link netlify into this directory instead of discovering one on PATH."`
	CacheNonce string `name:"cache-nonce" hidden:"" default:"" help:"Partition the Netlify CLI cache."`
}

// install installs the CLI, set up to act on the session's site. An API URL
// other than Netlify's, as tests pass, is handed to the CLI too.
func (f installFlags) install(ctx context.Context, client *sdk.Client, s session, apiURL string) (netlifycli.CLI, error) {
	version := f.CLIVersion
	if version == "" {
		version = netlifycli.DefaultVersion
	}
	cli, err := netlifycli.Installer{
		Dir:        expandHome(f.InstallDir),
		Version:    version,
		Client:     client,
		CacheNonce: f.CacheNonce,
		Log:        os.Stderr,
	}.Ensure(ctx)
	if err != nil {
		return cli, err
	}
	cli.Token = s.creds.Token
	cli.SiteID = s.site.ID
	if apiURL != netlify.DefaultAPIURL {
		cli.APIURL = apiURL
	}
	return cli, nil
}

// session is what every command starts from: credentials checked against the
// site they name.
type session struct {
	creds credentials
	api   *netlify.Client
	site  netlify.Site
	poll  time.Duration
}

func (f apiFlags) start(ctx context.Context) (session, error) {
	poll, err := parseDuration("poll-interval", f.PollInterval)
	if err != nil {
		return session{}, err
	}
	creds, err := readCredentials()
	if err != nil {
		return session{}, err
	}
	api := netlify.New(f.APIURL, creds.Token)
	site, err := api.Site(ctx, creds.SiteID)
	if err != nil {
		return session{}, siteError(err, creds)
	}
	return session{creds: creds, api: api, site: site, poll: poll}, nil
}

func (s session) waiter(timeout time.Duration) netlify.Waiter {
	return netlify.Waiter{Client: s.api, Timeout: timeout, Interval: s.poll, Log: os.Stderr}
}

// deploy fetches a deploy by ID and checks it belongs to the session's site.
func (s session) deploy(ctx context.Context, id string) (netlify.Deploy, error) {
	d, err := s.api.Deploy(ctx, id)
	if netlify.IsNotFound(err) || (err == nil && d.SiteID != "" && d.SiteID != s.site.ID) {
		return d, fmt.Errorf("netlify has no deploy %s on %s", id, s.site.Name)
	}
	return d, err
}

// publish makes d the deploy production serves and waits until it does.
func (s session) publish(ctx context.Context, d netlify.Deploy, timeout time.Duration) error {
	if err := s.api.Publish(ctx, s.site.ID, d.ID); err != nil {
		return fmt.Errorf("publish deploy %s: %w", d.ID, err)
	}
	return s.waiter(timeout).Published(ctx, s.site.ID, d.ID)
}

func siteError(err error, c credentials) error {
	var apiErr *netlify.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("look up the Netlify site: %w", err)
	}
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return fmt.Errorf("netlify rejected %s: create a new personal access token at https://app.netlify.com/user/applications (%w)", envToken, err)
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("%s cannot reach a site %s: check %s is the Project ID under Project configuration > General, and that the token's user is a member of its team (%w)",
			envToken, c.SiteID, envSiteID, err)
	}
	return fmt.Errorf("look up the Netlify site: %w", err)
}

// parseDuration reads a duration flag. Durations are strings in the config, so
// that the published flag type does not make a config check expect an integer.
func parseDuration(flag, s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--%s=%q: want a positive duration such as 10m or 90s", flag, s)
	}
	return d, nil
}

// expandHome resolves a leading ~ against the home directory, which kong leaves
// alone. The tag holds ~/ so function.yaml publishes a readable default.
func expandHome(path string) string {
	if path == "" || path[0] != '~' || (len(path) > 1 && !os.IsPathSeparator(path[1])) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// splitList reads a comma- or newline-separated flag, since a with: value is a
// single string.
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
