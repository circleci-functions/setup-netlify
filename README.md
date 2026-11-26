# setup-netlify

A CircleCI function that deploys to [Netlify](https://www.netlify.com), waits until the deploy is
ready, and promotes or rolls back production. It works for sites that deploy from CircleCI and
for sites that keep Netlify's Git integration.

```yaml
functions:
  netlify: github.com/circleci-functions/setup-netlify@v0.1.0

jobs:
  preview:
    docker:
      - image: cimg/node:lts
    steps:
      - checkout
      - netlify/deploy
      - run: npx playwright test --base-url "$DEPLOYMENT_URL"

  ship:
    docker:
      - image: cimg/node:lts
    steps:
      - checkout
      # Build with production's settings without publishing, test that exact
      # deploy, then point production at it.
      - netlify/deploy:
          with:
            environment: production
            staged: true
      - run: ./smoke.sh "$DEPLOYMENT_URL"
      - netlify/promote
```

The key under `functions:` names the steps, so `netlify:` gives `netlify/deploy`. Run
`circleci function add github.com/circleci-functions/setup-netlify` to write the entry.

## Credentials

Every command reads two environment variables, the same ones the Netlify CLI reads. Put them in a
context:

| Variable | Value |
|---|---|
| `NETLIFY_AUTH_TOKEN` | A personal access token from https://app.netlify.com/user/applications |
| `NETLIFY_SITE_ID` | The Project ID, under Project configuration > General in the Netlify app |

A missing variable fails the step before any work, naming it. The token is never printed.

## Commands

| Command | What it does |
|---|---|
| `setup` (a bare `- netlify` step) | Installs the Netlify CLI and puts `netlify` on PATH |
| `deploy` | Builds in the job, deploys, and waits until the deploy is ready |
| `wait-for-preview` | Waits for the deploy preview Netlify's Git integration builds for this commit. Deploys nothing |
| `promote` | Publishes a staged deploy to production |
| `rollback` | Publishes an earlier production deploy again |

`deploy`, `wait-for-preview`, `promote` and `rollback` export the deploy to the steps after them
in the same job:

| Variable | Value |
|---|---|
| `DEPLOYMENT_URL`, `NETLIFY_DEPLOY_URL` | `https://…` address of the deploy |
| `NETLIFY_DEPLOY_ID` | Its ID |
| `NETLIFY_ADMIN_URL` | Its page in the Netlify app, with the build log |

They also store a summary of the deploy as the artifact `setup-netlify/<command>.json`.

`setup` and `deploy` run the Netlify CLI, which needs Node.js 22.13 or later on PATH: use a
`cimg/node` image, or run `setup-node` first. The other commands call the Netlify API and need no
Node.js.

### setup

| Flag | Default | Description |
|---|---|---|
| `cli-version` | `27.20.1` | Netlify CLI version to install |

### deploy

| Flag | Default | Description |
|---|---|---|
| `environment` | `preview` | `preview` or `production`. Picks the environment variables the build reads, and whether production serves the deploy |
| `staged` | `false` | With `environment: production`, build with production's environment variables but leave production as it is, so the deploy can be tested and then promoted |
| `build` | `true` | Run the site's build command in the job. `false` uploads the publish directory as it is |
| `dir` | from `netlify.toml` | Directory to publish |
| `functions` | from `netlify.toml` | Directory of Netlify Functions to deploy |
| `alias` | | With `environment: preview`, publish at `https://<alias>--<site>.netlify.app` |
| `message` | | Title for the deploy in the Netlify app |
| `pass-env` | `DATABASE_URL` | Comma- or newline-separated names of job environment variables to hand to the deploy's functions, as secrets. Unset ones are skipped |
| `project-dir` | `.` | Directory holding `netlify.toml` |
| `timeout` | `10m` | How long to wait for the deploy to be ready |
| `cli-version` | `27.20.1` | Netlify CLI version to install |

The build runs with the job's environment, so a `DATABASE_URL` set by an earlier step reaches it.
`pass-env` also gives it to the deploy's functions at run time, for that deploy only.

### wait-for-preview

| Flag | Default | Description |
|---|---|---|
| `commit` | `$CIRCLE_SHA1` | Commit whose deploy preview to wait for |
| `timeout` | `10m` | How long to wait for the deploy preview to appear and be ready |

If no deploy preview appears, the error says whether the site is connected to a Git repository.

### promote

| Flag | Default | Description |
|---|---|---|
| `deployment` | | Deploy ID to publish. Empty uses `NETLIFY_DEPLOY_ID` from an earlier step, else this commit's staged deploy, so `promote` works in a later job too |
| `timeout` | `5m` | How long to wait for production to serve it |

Publishing a deploy does not rebuild it, so `promote` only takes deploys built for production:
deploy with `environment: production` and `staged: true`, as in the example above. A preview was
built with the preview's environment variables, and is refused.

### rollback

| Flag | Default | Description |
|---|---|---|
| `deployment` | | Deploy ID to roll back to. Empty uses the deploy production served before the current one |
| `timeout` | `5m` | How long to wait for production to serve it |

## Platforms

Linux, macOS and Windows, on amd64 and arm64. On Windows, `netlify` goes on PATH through
`$BASH_ENV`, so it resolves in later `bash` steps.

## Releasing

See [RELEASE.md](RELEASE.md).
