package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func resolveWailsProjectRoot() (string, error) {
	candidates := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, cwd)
	}
	if exePath, err := os.Executable(); err == nil && exePath != "" {
		candidates = append(candidates, filepath.Dir(exePath))
	}

	checked := make(map[string]struct{}, 16)
	for _, candidate := range candidates {
		root := filepath.Clean(candidate)
		for {
			if _, seen := checked[root]; !seen {
				checked[root] = struct{}{}
				if fileExists(filepath.Join(root, "wails.json")) {
					return root, nil
				}
			}

			parent := filepath.Dir(root)
			if parent == root {
				break
			}
			root = parent
		}
	}

	return "", fmt.Errorf("wails.json not found from current working directory or executable path")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
