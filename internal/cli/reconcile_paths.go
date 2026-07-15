package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveFile(explicit, pattern, configDir string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("file %q not found", explicit)
		}
		return explicit, nil
	}
	if pattern == "" {
		return "", fmt.Errorf("no file specified and no file_pattern configured")
	}
	resolvedPattern := pattern
	if !filepath.IsAbs(resolvedPattern) {
		resolvedPattern = filepath.Join(configDir, resolvedPattern)
	}
	matches, err := filepath.Glob(resolvedPattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", resolvedPattern, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no files match pattern %q", resolvedPattern)
	}
	if filepath.IsAbs(pattern) {
		return matches[0], nil
	}
	for _, match := range matches {
		inside, err := pathWithinDir(configDir, match)
		if err != nil {
			return "", err
		}
		if inside {
			return match, nil
		}
	}
	return "", fmt.Errorf("file_pattern %q resolves outside config directory %q", pattern, configDir)
}

func pathWithinDir(dir, path string) (bool, error) {
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("resolve config directory: %w", err)
	}
	dirAbs, err = filepath.EvalSymlinks(dirAbs)
	if err != nil {
		return false, fmt.Errorf("resolve config directory symlinks: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve matched file: %w", err)
	}
	pathAbs, err = filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return false, fmt.Errorf("resolve matched file symlinks: %w", err)
	}
	rel, err := filepath.Rel(dirAbs, pathAbs)
	if err != nil {
		return false, fmt.Errorf("compare matched file to config directory: %w", err)
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !startsWithParent(rel)), nil
}

func startsWithParent(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}
