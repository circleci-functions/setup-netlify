// Package netlifycli installs a pinned Netlify CLI with npm and runs it.
package netlifycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/pathlink"
	"github.com/circleci-functions/toolkit/sdk"
)

// DefaultVersion is the Netlify CLI installed when no version is given.
const DefaultVersion = "27.20.1"

// The oldest Node.js the Netlify CLI supports.
const (
	minNodeMajor = 22
	minNodeMinor = 13
)

// Installer installs one version of the CLI under Dir, through the job cache.
type Installer struct {
	// Dir holds one directory per installed version.
	Dir     string
	Version string
	Client  *sdk.Client
	// CacheNonce partitions the cache, so a test cannot restore an earlier
	// run's install.
	CacheNonce string
	Log        io.Writer
}

// CLI is an installed Netlify CLI.
type CLI struct {
	// Prefix is the npm prefix it is installed under.
	Prefix  string
	Version string
	// Token and SiteID reach the CLI as NETLIFY_AUTH_TOKEN and NETLIFY_SITE_ID,
	// so the token never appears in its arguments.
	Token  string
	SiteID string
	// APIURL, when set, sends the CLI's API calls there instead of Netlify's.
	APIURL string
	Log    io.Writer
}

// Ensure returns the CLI, installing it if neither an earlier run nor the job
// cache has it.
func (i Installer) Ensure(ctx context.Context) (CLI, error) {
	if err := checkNode(ctx); err != nil {
		return CLI{}, err
	}
	prefix := filepath.Join(i.Dir, i.Version)
	cli := CLI{Prefix: prefix, Version: i.Version, Log: i.Log}
	if installed(prefix) {
		return cli, nil
	}

	key := i.cacheKey()
	_, err := i.Client.RestoreCache(ctx, []string{key}, sdk.WithStepName("Restoring Netlify CLI "+i.Version))
	sdk.LogCacheErr("restore", err)
	if installed(prefix) {
		fmt.Fprintf(i.Log, "setup-netlify: restored Netlify CLI %s from cache\n", i.Version)
		return cli, nil
	}

	fmt.Fprintf(i.Log, "setup-netlify: installing Netlify CLI %s\n", i.Version)
	cmd := exec.CommandContext(ctx, "npm", "install", "--global", "--prefix", prefix,
		"--no-audit", "--no-fund", "--no-update-notifier", "--loglevel=error", "netlify-cli@"+i.Version)
	cmd.Stdout = i.Log
	cmd.Stderr = i.Log
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(prefix)
		return CLI{}, fmt.Errorf("install Netlify CLI %s with npm: %w", i.Version, err)
	}
	if !installed(prefix) {
		return CLI{}, fmt.Errorf("npm installed netlify-cli@%s, but %s is missing", i.Version, Bin(prefix))
	}
	sdk.LogCacheErr("save", i.Client.SaveCache(ctx, key, []string{prefix}, sdk.WithStepName("Saving Netlify CLI "+i.Version)))
	return cli, nil
}

func (i Installer) cacheKey() string {
	key := fmt.Sprintf("setup-netlify-cli-v1-%s-%s", sdk.OSArch(), i.Version)
	if i.CacheNonce != "" {
		key += "-" + i.CacheNonce
	}
	return key
}

// Bin returns the path of the netlify executable under an npm prefix.
func Bin(prefix string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "netlify.cmd")
	}
	return filepath.Join(prefix, "bin", "netlify")
}

func installed(prefix string) bool {
	_, err := os.Stat(Bin(prefix))
	return err == nil
}

// Link makes netlify resolvable by name in later steps, and returns where it
// resolves from. binDir, when set, links into exactly that directory.
func (c CLI) Link(binDir string) (string, error) {
	if runtime.GOOS == "windows" {
		// npm's Windows shims find node_modules relative to themselves, so
		// they cannot be linked elsewhere; put the prefix on PATH instead, in
		// the form the job's bash reads.
		if err := bashenv.AddToPath(bashPath(c.Prefix)); err != nil {
			return "", err
		}
		return c.Prefix, nil
	}
	res, err := pathlink.Ensure(pathlink.Options{
		Names:     []string{"netlify"},
		FromDir:   filepath.Dir(Bin(c.Prefix)),
		Preferred: pathlink.UserBinDirs(),
		Dir:       binDir,
		AddToPath: bashenv.AddToPath,
		Verify: func(bin string) error {
			out, err := pathlink.Command(bin, "--version").CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to run: %w", err)
			}
			if !strings.Contains(string(out), c.Version) {
				return fmt.Errorf("reports %q rather than %s", strings.TrimSpace(string(out)), c.Version)
			}
			return nil
		},
	})
	return res.Dir, err
}

// bashPath turns a Windows path into the form Git Bash puts on PATH, since a
// drive letter's colon would split a PATH entry: C:\Users\x becomes /c/Users/x.
func bashPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// Run runs netlify in dir with args, streaming its stderr to the log, and
// returns its stdout. Stdout is streamed too unless quiet, for commands whose
// stdout is JSON to parse.
func (c CLI) Run(ctx context.Context, dir string, quiet bool, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, Bin(c.Prefix), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"NETLIFY_AUTH_TOKEN="+c.Token,
		"NETLIFY_SITE_ID="+c.SiteID,
		"NETLIFY_TELEMETRY_DISABLED=1",
		"NO_UPDATE_NOTIFIER=1",
	)
	if c.APIURL != "" {
		cmd.Env = append(cmd.Env, "NETLIFY_API_URL="+c.APIURL)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if !quiet {
		cmd.Stdout = io.MultiWriter(&stdout, c.Log)
	}
	cmd.Stderr = c.Log
	if err := cmd.Run(); err != nil {
		if quiet {
			// What the CLI printed is the only account of why it failed.
			fmt.Fprint(c.Log, stdout.String())
		}
		return stdout.String(), fmt.Errorf("netlify %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// Result is what netlify deploy --json prints.
type Result struct {
	SiteID    string `json:"site_id"`
	SiteName  string `json:"site_name"`
	DeployID  string `json:"deploy_id"`
	DeployURL string `json:"deploy_url"`
	URL       string `json:"url"`
	Logs      string `json:"logs"`
}

// ParseResult reads the deploy netlify deploy --json made from its stdout. The
// JSON is the last thing printed; anything before it is skipped.
func ParseResult(stdout string) (Result, error) {
	var r Result
	s := strings.TrimSpace(stdout)
	if i := strings.LastIndex(s, "\n{"); i >= 0 {
		s = s[i+1:]
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil || r.DeployID == "" {
		return r, errors.New("netlify deploy printed no deploy ID")
	}
	return r, nil
}

// checkNode fails unless a Node.js the CLI supports is on PATH.
func checkNode(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "node", "--version").Output()
	want := fmt.Sprintf("%d.%d", minNodeMajor, minNodeMinor)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("the Netlify CLI needs Node.js %s or later, and node is not on PATH: run setup-node, or use a cimg/node image, before this step", want)
		}
		return fmt.Errorf("run node --version: %w", err)
	}
	got := strings.TrimSpace(string(out))
	major, minor, ok := nodeVersion(got)
	if !ok {
		return fmt.Errorf("could not read the Node.js version from %q", got)
	}
	if major < minNodeMajor || (major == minNodeMajor && minor < minNodeMinor) {
		return fmt.Errorf("the Netlify CLI needs Node.js %s or later, and node on PATH is %s: run setup-node before this step", want, got)
	}
	return nil
}

func nodeVersion(s string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(s, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
}
