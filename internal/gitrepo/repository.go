// Package gitrepo contains the Go Git integration for the status checker.
package gitrepo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

type Repository struct {
	Path string
	// GitDir is the metadata directory shared by linked worktrees.
	GitDir string
	repo   *git.Repository
}

// Open loads the checkout at path and resolves its common Git directory.
func Open(path string) (*Repository, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, git.ErrRepositoryNotExists
		}
		return nil, err
	}
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{EnableDotGitCommonDir: true})
	if err != nil {
		return nil, err
	}
	if _, err := repo.Worktree(); err != nil {
		return nil, err
	}

	// Linked worktrees have different HEADs but share objects and tracking refs.
	dotGit := repo.Storer.(*filesystem.Storage).Filesystem().Root()
	common, err := os.ReadFile(filepath.Join(dotGit, "commondir"))
	switch {
	case err == nil:
		dir := strings.TrimSpace(string(common))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(dotGit, dir)
		}
		dotGit = dir
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	dotGit, err = filepath.EvalSymlinks(dotGit)
	if err != nil {
		return nil, err
	}
	return &Repository{Path: path, GitDir: dotGit, repo: repo}, nil
}
