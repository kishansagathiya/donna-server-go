package skills

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

//go:embed bundled/*.md
var bundledFS embed.FS

const SourceSystem = "system"

// Bundled returns the system skills compiled into the binary. Invalid files
// are logged and skipped (they fail PR review, not startup).
func Bundled() []Skill {
	entries, err := bundledFS.ReadDir("bundled")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]Skill, 0, len(names))
	for _, name := range names {
		raw, err := bundledFS.ReadFile("bundled/" + name)
		if err != nil {
			continue
		}
		s, err := ParseSkillMD(string(raw))
		if err != nil {
			log.Warn("bundled skill invalid, skipping", map[string]any{"file": name, "error": err.Error()})
			continue
		}
		s.Source = SourceSystem
		out = append(out, s)
	}
	return out
}

// System returns a bundled skill by name, if present.
func System(name string) (Skill, bool) {
	for _, s := range Bundled() {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// FormatBundled renders bundled skills for logs/debug.
func FormatBundled() string {
	var b strings.Builder
	for _, s := range Bundled() {
		fmt.Fprintf(&b, "%s (%d chars), ", s.Name, len(s.Content))
	}
	return strings.TrimSuffix(b.String(), ", ")
}
