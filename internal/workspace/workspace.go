package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type Permission interface {
	EnsurePathAccess(ctx context.Context, targetPath, intent string) error
}

func Resolve(ctx context.Context, cwd, targetPath, intent string, permission Permission) (string, error) {
	resolved, err := filepath.Abs(filepath.Join(cwd, targetPath))
	if err != nil {
		return "", err
	}

	if permission != nil {
		if err := permission.EnsurePathAccess(ctx, resolved, intent); err != nil {
			return "", err
		}
		return resolved, nil
	}

	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if !Within(root, resolved) {
		return "", fmt.Errorf("Path escapes workspace: %s", targetPath)
	}
	return resolved, nil
}

func Within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
