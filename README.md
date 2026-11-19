# repo-check

`repo-check` fetches the refs needed to check the current branch across your registered Git repositories. It runs several repository checks concurrently and prints results in path order.

## Install

With Go 1.26 or newer, run this from the project directory:

```sh
go install ./cmd/repo-check
```

Ensure the Go binary directory is on your `PATH`. It defaults to `$(go env GOPATH)/bin` when `GOBIN` is unset.

## Use

```sh
repo-check add ~/Development/my-project
repo-check add --folder ~/Development
repo-check
repo-check check my-project
repo-check check --jobs 2 ~/Development/my-project
```

Registrations are additive. A registered folder is scanned one level deep on each run. The configuration is stored in your user config directory, under `repo-check/config.json`. Set `REPO_CHECK_CONFIG` to use a different file.

For each configured remote, the report shows the branch matching your current branch name and the remote's advertised default branch. It marks the configured upstream and gives it a separate row when its name differs. A fork with `origin` and `upstream` configured shows both. A default branch is missing commits when its tip is outside the local branch's history.

Checks fetch objects and refresh the relevant remote-tracking refs. When a repo has no tracking mapping for a requested branch, the checker stores its tip under `refs/repo-check/`. Checks leave the current branch, staging area, and working files intact. Divergence is printed as a warning. An unavailable comparison or fetch error is reported separately and makes the command exit with status 1. For unequal tips in a shallow checkout, the tool reports the comparison as unavailable.

## SSH

The checker uses go-git's SSH transport with your SSH agent and known hosts. For a private remote, run the checker from a session with the right key available to the agent. The project currently implements fetch and status.
