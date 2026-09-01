package localagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWorkspacePath canonicalizes rel (or an absolute path) against the
// workspace root and rejects traversal, symlink escapes, and empty roots.
func ResolveWorkspacePath(root, rel string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace_required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("workspace_invalid")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("workspace_invalid")
		}
		resolvedRoot = absRoot
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("workspace_unavailable")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace_not_directory")
	}

	target := strings.TrimSpace(rel)
	if target == "" || target == "." {
		return resolvedRoot, nil
	}
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute_path_forbidden")
	}
	joined := filepath.Join(resolvedRoot, target)
	cleaned, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("path_invalid")
	}
	// If the path exists, resolve symlinks and verify they stay inside root.
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("path_invalid")
	}
	relOut, err := filepath.Rel(resolvedRoot, cleaned)
	if err != nil {
		return "", fmt.Errorf("path_escape")
	}
	if relOut == ".." || strings.HasPrefix(relOut, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path_escape")
	}
	return cleaned, nil
}
