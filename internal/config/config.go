// Package config stores repo-check's registry of checkouts and parent folders.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Registry holds explicit checkout paths and parent folders to scan for checkouts.
type Registry struct {
	Repositories []string `json:"repositories,omitempty"`
	Folders      []string `json:"folders,omitempty"`
}

// Path returns the default registry filename under the user's config directory.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repo-check", "config.json"), nil
}

// Load reads a JSON registry from path. A missing file represents an empty registry.
func Load(path string) (Registry, error) {
	var registry Registry
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	return registry, json.Unmarshal(data, &registry)
}

// Add records path once in Repositories, or in Folders if folder is true.
func (r *Registry) Add(path string, folder bool) {
	paths := &r.Repositories
	if folder {
		paths = &r.Folders
	}
	if !slices.Contains(*paths, path) {
		*paths = append(*paths, path)
	}
}

// Save replaces the registry only after writing and closing the new JSON file.
func (r Registry) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(r)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// CanonicalPath returns an absolute path with symlinks resolved.
func CanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
