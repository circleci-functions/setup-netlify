// Package handoff passes a deploy to the steps after setup-netlify: as
// environment variables, and as a summary stored with the job's artifacts.
package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"

	"github.com/circleci-functions/setup-netlify/netlify"
)

// The variables later steps read the deploy from.
const (
	// EnvURL is named for any deploy target, so a test step does not need to
	// know which one the job deploys to.
	EnvURL       = "DEPLOYMENT_URL"
	EnvDeployURL = "NETLIFY_DEPLOY_URL"
	EnvID        = "NETLIFY_DEPLOY_ID"
	EnvAdminURL  = "NETLIFY_ADMIN_URL"
)

// Summary is the deploy record stored as an artifact.
type Summary struct {
	Command   string `json:"command"`
	URL       string `json:"url"`
	DeployID  string `json:"deploy_id"`
	AdminURL  string `json:"admin_url,omitempty"`
	Context   string `json:"context"`
	Published bool   `json:"published"`
}

// Publish exports d to later steps, at url, and stores its summary as an
// artifact. Only a failure to export fails it: outside a job there is nothing
// to export to, and a missing artifact is not worth failing a deploy over.
func Publish(ctx context.Context, client *sdk.Client, command string, d netlify.Deploy, url string, published bool, log io.Writer) error {
	if url == "" {
		url = d.URL()
	}
	fmt.Fprintf(log, "setup-netlify: %s\n", url)

	err := bashenv.ExportEnvVars(map[string]string{
		EnvURL:       url,
		EnvDeployURL: url,
		EnvID:        d.ID,
		EnvAdminURL:  d.LogsURL(),
	})
	switch {
	case errors.Is(err, bashenv.ErrNoBashEnv):
		fmt.Fprintf(log, "setup-netlify: BASH_ENV is not set, so %s is not exported to later steps\n", EnvURL)
	case err != nil:
		return fmt.Errorf("export the deploy to later steps: %w", err)
	}

	storeSummary(ctx, client, Summary{
		Command:   command,
		URL:       url,
		DeployID:  d.ID,
		AdminURL:  d.LogsURL(),
		Context:   d.Context,
		Published: published,
	}, log)
	return nil
}

func storeSummary(ctx context.Context, client *sdk.Client, s Summary, log io.Writer) {
	dir, err := os.MkdirTemp("", "setup-netlify-")
	if err != nil {
		fmt.Fprintf(log, "setup-netlify: could not write the deploy summary: %v\n", err)
		return
	}
	path := filepath.Join(dir, "deploy.json")
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		fmt.Fprintf(log, "setup-netlify: could not write the deploy summary: %v\n", err)
		return
	}
	err = client.StoreArtifacts(ctx, path, "setup-netlify/"+s.Command+".json")
	if err != nil && !errors.Is(err, sdk.ErrNoServer) {
		fmt.Fprintf(log, "setup-netlify: could not store the deploy summary: %v\n", err)
	}
}
