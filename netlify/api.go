// Package netlify is a small client for the parts of the Netlify API that
// setup-netlify needs: sites, deploys, and publishing a deploy.
package netlify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIURL is the Netlify API.
const DefaultAPIURL = "https://api.netlify.com/api/v1"

// Deploy states this package acts on. Netlify reports others while a deploy is
// in progress (new, enqueued, building, uploading, processing and more).
const (
	StateReady    = "ready"
	StateError    = "error"
	StateRejected = "rejected"
)

// ContextProduction is the deploy context of a production deploy.
const ContextProduction = "production"

// Deploy is the subset of a Netlify deploy setup-netlify reads.
type Deploy struct {
	ID           string `json:"id"`
	SiteID       string `json:"site_id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	Context      string `json:"context"`
	Title        string `json:"title"`
	CommitRef    string `json:"commit_ref"`
	Branch       string `json:"branch"`
	DeploySSLURL string `json:"deploy_ssl_url"`
	SSLURL       string `json:"ssl_url"`
	AdminURL     string `json:"admin_url"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at"`
	PublishedAt  string `json:"published_at"`
	Skipped      *bool  `json:"skipped"`
	Locked       *bool  `json:"locked"`
}

// URL returns the deploy's own address, which keeps serving this deploy
// whatever production points at later.
func (d Deploy) URL() string {
	if d.DeploySSLURL != "" {
		return d.DeploySSLURL
	}
	return d.SSLURL
}

// LogsURL returns the deploy's page in the Netlify app.
func (d Deploy) LogsURL() string {
	if d.AdminURL == "" || d.ID == "" {
		return ""
	}
	return strings.TrimRight(d.AdminURL, "/") + "/deploys/" + d.ID
}

// WasPublished reports whether the deploy has ever served production.
func (d Deploy) WasPublished() bool {
	return d.PublishedAt != ""
}

// Site is the subset of a Netlify site (a project, in the Netlify app) that
// setup-netlify reads.
type Site struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	SSLURL          string  `json:"ssl_url"`
	AdminURL        string  `json:"admin_url"`
	PublishedDeploy *Deploy `json:"published_deploy"`
	BuildSettings   *struct {
		RepoURL string `json:"repo_url"`
	} `json:"build_settings"`
}

// PublishedID returns the ID of the deploy production serves, or "".
func (s Site) PublishedID() string {
	if s.PublishedDeploy == nil {
		return ""
	}
	return s.PublishedDeploy.ID
}

// GitLinked reports whether the site builds from a Git repository.
func (s Site) GitLinked() bool {
	return s.BuildSettings != nil && s.BuildSettings.RepoURL != ""
}

// APIError is a non-2xx answer from the Netlify API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("netlify API: %s (%d)", msg, e.Status)
}

// IsNotFound reports whether err is a 404 from the Netlify API.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Client calls the Netlify API with one token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client for baseURL, which includes the /api/v1 prefix.
func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Site fetches a site by its ID.
func (c *Client) Site(ctx context.Context, id string) (Site, error) {
	var s Site
	err := c.do(ctx, http.MethodGet, "/sites/"+url.PathEscape(id), &s)
	return s, err
}

// Deploy fetches a deploy by its ID.
func (c *Client) Deploy(ctx context.Context, id string) (Deploy, error) {
	var d Deploy
	err := c.do(ctx, http.MethodGet, "/deploys/"+url.PathEscape(id), &d)
	return d, err
}

// ListDeploys lists a site's most recent deploys, newest first.
func (c *Client) ListDeploys(ctx context.Context, siteID string, perPage int) ([]Deploy, error) {
	if perPage <= 0 {
		perPage = 50
	}
	var ds []Deploy
	path := fmt.Sprintf("/sites/%s/deploys?per_page=%d", url.PathEscape(siteID), perPage)
	err := c.do(ctx, http.MethodGet, path, &ds)
	return ds, err
}

// Publish makes deployID the deploy production serves. Netlify swaps it in
// without rebuilding it.
func (c *Client) Publish(ctx context.Context, siteID, deployID string) error {
	path := fmt.Sprintf("/sites/%s/deploys/%s/restore", url.PathEscape(siteID), url.PathEscape(deployID))
	return c.do(ctx, http.MethodPost, path, nil)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "setup-netlify")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("netlify API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return apiError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("netlify API: decode %s %s: %w", method, path, err)
	}
	return nil
}

func apiError(resp *http.Response) error {
	var body struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &body) != nil {
		body.Message = strings.TrimSpace(string(b))
	}
	msg := body.Message
	if msg == "" {
		msg = body.Error
	}
	return &APIError{Status: resp.StatusCode, Message: msg}
}
