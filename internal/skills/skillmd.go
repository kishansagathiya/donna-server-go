package skills

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxNameLength        = 64
	MaxDescriptionLength = 500
	MaxContentLength     = 16_384
)

// SkillNamePattern is the agentskills.io slug rule: lowercase letters, digits,
// hyphens; must start and end with an alphanumeric.
var SkillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill is one procedure, portable with agentskills.io SKILL.md files.
// ID is empty for bundled system skills.
type Skill struct {
	ID          string  `json:"id,omitempty"`
	UserID      string  `json:"user_id,omitempty"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Content     string  `json:"content"`
	Source      string  `json:"source"` // user | agent | system
	AgentRunID  *string `json:"agent_run_id,omitempty"`
	Version     int     `json:"version,omitempty"`
	CreatedAt   string  `json:"created_at,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
}

// Validate checks name/description/content limits.
func (s Skill) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return fmt.Errorf("name_required")
	}
	if len(s.Name) > MaxNameLength {
		return fmt.Errorf("name_too_long")
	}
	if !SkillNamePattern.MatchString(s.Name) {
		return fmt.Errorf("invalid_name")
	}
	if len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("description_too_long")
	}
	if len(s.Content) > MaxContentLength {
		return fmt.Errorf("content_too_long")
	}
	return nil
}

// RenderSkillMD renders a skill as an agentskills.io-compatible SKILL.md
// (YAML frontmatter with name + description, then markdown body).
func RenderSkillMD(s Skill) string {
	name := strings.TrimSpace(s.Name)
	desc := strings.TrimSpace(s.Description)
	var descVal string
	if strings.ContainsAny(desc, "\n:\"") {
		descVal = quoteYAML(desc)
	} else {
		descVal = desc
	}
	body := strings.TrimSpace(s.Content)
	if body == "" {
		body = "No content."
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, descVal, body)
}

func quoteYAML(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ParseSkillMD parses an agentskills.io-compatible SKILL.md document:
// optional YAML frontmatter (--- delimited) with name and description,
// followed by the markdown body.
func ParseSkillMD(raw string) (Skill, error) {
	out := Skill{}
	text := strings.TrimSpace(raw)
	if text == "" {
		return out, fmt.Errorf("empty_skill")
	}
	if strings.HasPrefix(text, "---") {
		end := strings.Index(text[3:], "\n---")
		if end >= 0 {
			fm := text[3 : end+3]
			body := strings.TrimSpace(text[end+4:])
			body = strings.TrimPrefix(body, "---")
			body = strings.TrimSpace(body)
			var meta struct {
				Name        string `yaml:"name" json:"name"`
				Description string `yaml:"description" json:"description"`
			}
			if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
				return out, fmt.Errorf("invalid_frontmatter: %w", err)
			}
			out.Name = strings.TrimSpace(meta.Name)
			out.Description = strings.TrimSpace(meta.Description)
			out.Content = body
		} else {
			return out, fmt.Errorf("unterminated_frontmatter")
		}
	} else {
		out.Content = text
	}
	if err := out.Validate(); err != nil {
		// Missing name in frontmatter is the common case; surface it clearly.
		if strings.TrimSpace(out.Name) == "" {
			return Skill{}, fmt.Errorf("name_required")
		}
		return Skill{}, err
	}
	return out, nil
}
