package netlify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Waiter polls the API until a deploy or a publish settles.
type Waiter struct {
	Client   *Client
	Timeout  time.Duration
	Interval time.Duration
	Log      io.Writer
}

// Ready waits for the deploy to be ready, and fails if it errors, is skipped or
// rejected, or is still in progress at the timeout.
func (w Waiter) Ready(ctx context.Context, id string) (Deploy, error) {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	var last Deploy
	for {
		d, err := w.Client.Deploy(ctx, id)
		if err != nil {
			if ctx.Err() != nil && last.ID != "" {
				return last, w.timedOut(last)
			}
			return d, err
		}
		if d.State != last.State {
			fmt.Fprintf(w.Log, "setup-netlify: deploy %s is %s\n", d.ID, d.State)
		}
		last = d

		switch {
		case d.Skipped != nil && *d.Skipped:
			return d, failed(d, "was skipped by the site's ignore command")
		case d.State == StateReady:
			return d, nil
		case d.State == StateError:
			return d, failed(d, "failed")
		case d.State == StateRejected:
			return d, failed(d, "was rejected")
		}

		if err := sleep(ctx, w.Interval); err != nil {
			return d, w.timedOut(d)
		}
	}
}

// ForCommit waits for a deploy preview or branch deploy of sha to appear, as
// Netlify's Git integration builds them, then for it to be ready.
func (w Waiter) ForCommit(ctx context.Context, site Site, sha string) (Deploy, error) {
	deadline := time.Now().Add(w.Timeout)
	findCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	fmt.Fprintf(w.Log, "setup-netlify: looking for the deploy preview of %s\n", sha)
	for {
		ds, err := w.Client.ListDeploys(findCtx, site.ID, 50)
		if err != nil && findCtx.Err() == nil {
			return Deploy{}, err
		}
		if d, ok := previewOf(ds, sha); ok {
			w.Timeout = time.Until(deadline)
			return w.Ready(ctx, d.ID)
		}
		if err := sleep(findCtx, w.Interval); err != nil {
			return Deploy{}, noPreview(site, sha, w.Timeout)
		}
	}
}

// previewOf returns the newest deploy of sha that is not a production deploy.
func previewOf(ds []Deploy, sha string) (Deploy, bool) {
	for _, d := range ds {
		if d.CommitRef == sha && d.Context != ContextProduction {
			return d, true
		}
	}
	return Deploy{}, false
}

// Published waits until production serves deployID.
func (w Waiter) Published(ctx context.Context, siteID, deployID string) error {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()

	for {
		s, err := w.Client.Site(ctx, siteID)
		if err == nil && s.PublishedID() == deployID {
			return nil
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
		if err := sleep(ctx, w.Interval); err != nil {
			return fmt.Errorf("production does not serve deploy %s after %s", deployID, w.Timeout)
		}
	}
}

func (w Waiter) timedOut(d Deploy) error {
	msg := fmt.Sprintf("deploy %s is still %s after %s; it keeps running on Netlify", d.ID, d.State, w.Timeout)
	if u := d.LogsURL(); u != "" {
		msg += ": " + u
	}
	return errors.New(msg)
}

func failed(d Deploy, what string) error {
	msg := fmt.Sprintf("deploy %s %s", d.ID, what)
	if d.ErrorMessage != "" {
		msg += ": " + d.ErrorMessage
	}
	if u := d.LogsURL(); u != "" {
		msg += "\nsee its log at " + u
	}
	return errors.New(msg)
}

func noPreview(s Site, sha string, timeout time.Duration) error {
	msg := fmt.Sprintf("no deploy preview of %s appeared on %s within %s", sha, s.Name, timeout)
	if !s.GitLinked() {
		return errors.New(msg + "; the site is not connected to a Git repository, so Netlify only deploys it when asked: use deploy instead")
	}
	return errors.New(msg + "; the site builds from Git, so check that deploy previews or branch deploys are on for this branch, and whether the ignore command skipped the commit")
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
