// Package check selects registered checkouts and coordinates their checks.
package check

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/matteoroda05/repo-check/internal/config"
	"github.com/matteoroda05/repo-check/internal/gitrepo"
)

func Discover(registry config.Registry) ([]string, error) {
	paths := make(map[string]bool)
	add := func(path string) {
		if canonical, err := config.CanonicalPath(path); err == nil {
			path = canonical
		}
		paths[path] = true
	}
	for _, path := range registry.Repositories {
		add(path)
	}

	var problems []error
	for _, folder := range registry.Folders {
		children, err := os.ReadDir(folder)
		if err != nil {
			problems = append(problems, fmt.Errorf("scan %s: %w", folder, err))
			continue
		}
		for _, child := range children {
			path := filepath.Join(folder, child.Name())
			if !child.IsDir() && child.Type()&os.ModeSymlink != 0 {
				info, err := os.Stat(path)
				if err != nil || !info.IsDir() {
					continue
				}
			} else if !child.IsDir() {
				continue
			}
			_, err := os.Stat(filepath.Join(path, ".git"))
			if err == nil {
				add(path)
			} else if !errors.Is(err, os.ErrNotExist) {
				problems = append(problems, err)
			}
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	slices.Sort(result)
	return result, errors.Join(problems...)
}

func Select(paths, selectors []string) ([]string, error) {
	if len(selectors) == 0 {
		return paths, nil
	}
	selected := make(map[string]bool)
	for _, selector := range selectors {
		if strings.ContainsRune(selector, filepath.Separator) || selector == "." || selector == ".." {
			path, err := config.CanonicalPath(selector)
			if err != nil {
				path, _ = filepath.Abs(selector)
			}
			if !slices.Contains(paths, path) {
				return nil, fmt.Errorf("no registered repository matches %q", selector)
			}
			selected[path] = true
			continue
		}
		var match string
		for _, candidate := range paths {
			if filepath.Base(candidate) != selector {
				continue
			}
			if match != "" {
				return nil, fmt.Errorf("%q matches more than one repository; select by path", selector)
			}
			match = candidate
		}
		if match == "" {
			return nil, fmt.Errorf("no registered repository matches %q", selector)
		}
		selected[match] = true
	}
	var result []string
	for _, path := range paths {
		if selected[path] {
			result = append(result, path)
		}
	}
	return result, nil
}

type Result struct {
	Path   string
	Status gitrepo.Status
}

func Run(ctx context.Context, paths []string, workers int) []Result {
	type job struct {
		index int
		repo  *gitrepo.Repository
	}
	results := make([]Result, len(paths))
	groups := make(map[string][]job)
	for i, path := range paths {
		results[i].Path = path
		repo, err := gitrepo.Open(path)
		if err != nil {
			results[i].Status.Err = err
			continue
		}
		groups[repo.GitDir] = append(groups[repo.GitDir], job{index: i, repo: repo})
	}

	queue := make(chan []job)
	var workersDone sync.WaitGroup
	for range min(max(workers, 1), len(groups)) {
		workersDone.Go(func() {
			for group := range queue {
				for _, task := range group {
					if err := ctx.Err(); err != nil {
						results[task.index].Status.Err = err
					} else {
						results[task.index].Status = task.repo.Check(ctx)
					}
				}
			}
		})
	}
	for _, group := range groups {
		queue <- group
	}
	close(queue)
	workersDone.Wait()
	return results
}
