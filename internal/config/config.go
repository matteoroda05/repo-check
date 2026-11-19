package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

type Registry struct {
	Repositories []string `json:"repositories,omitempty"`
	Folders      []string `json:"folders,omitempty"`
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "repo-check", "config.json"), nil
}

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

func (r *Registry) Add(path string, folder bool) {
	paths := &r.Repositories
	if folder {
		paths = &r.Folders
	}
	if !slices.Contains(*paths, path) {
		*paths = append(*paths, path)
	}
}

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

// CanonicalPath makes registrations independent of the working directory and
// identifies multiple paths to the same checkout.
func CanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
