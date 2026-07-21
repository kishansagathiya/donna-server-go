package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// TaxonomyTag is a canonical (or aliased) tag in the user's vocabulary.
type TaxonomyTag struct {
	Name           string  `json:"name"`
	Count          int     `json:"count"`
	NormalizedName string  `json:"normalized_name,omitempty"`
	AliasOf        *string `json:"alias_of,omitempty"`
	Pinned         bool    `json:"pinned"`
}

// NoteTagDetail includes origin/locked for UI and enrichment.
type NoteTagDetail struct {
	Tag    string `json:"tag"`
	Origin string `json:"origin"`
	Locked bool   `json:"locked"`
}

// NormalizeTagUnicode lowercases and collapses whitespace (Unicode-aware).
func NormalizeTagUnicode(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if prevSpace {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// ListTaxonomy returns the user's tags including canonical metadata.
func (n *Notes) ListTaxonomy(ctx context.Context, userID string, limit int) ([]TaxonomyTag, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	q := url.Values{}
	q.Set("select", "name,count,normalized_name,alias_of,pinned")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "pinned.desc,count.desc,name.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []TaxonomyTag
	if err := n.DB.Get(ctx, "tags", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ResolveCanonical maps a candidate tag through aliases / normalized_name.
func (n *Notes) ResolveCanonical(ctx context.Context, userID, candidate string) (string, error) {
	normed := NormalizeTagUnicode(candidate)
	if normed == "" {
		return "", nil
	}
	taxonomy, err := n.ListTaxonomy(ctx, userID, 500)
	if err != nil {
		return "", err
	}
	byName := map[string]TaxonomyTag{}
	byNorm := map[string]TaxonomyTag{}
	for _, t := range taxonomy {
		byName[t.Name] = t
		key := t.NormalizedName
		if key == "" {
			key = NormalizeTagUnicode(t.Name)
		}
		byNorm[key] = t
	}
	if t, ok := byName[normed]; ok {
		if t.AliasOf != nil && strings.TrimSpace(*t.AliasOf) != "" {
			return NormalizeTagUnicode(*t.AliasOf), nil
		}
		return t.Name, nil
	}
	if t, ok := byNorm[normed]; ok {
		if t.AliasOf != nil && strings.TrimSpace(*t.AliasOf) != "" {
			return NormalizeTagUnicode(*t.AliasOf), nil
		}
		return t.Name, nil
	}
	return normed, nil
}

// ApplyAutoTags replaces unlocked auto tags while preserving locked ones.
func (n *Notes) ApplyAutoTags(ctx context.Context, userID, noteID string, tags []string) ([]string, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, fmt.Errorf("notes disabled")
	}
	cleaned := make([]string, 0, len(tags))
	for _, t := range tags {
		c, err := n.ResolveCanonical(ctx, userID, t)
		if err != nil {
			return nil, err
		}
		if c == "" {
			continue
		}
		cleaned = append(cleaned, c)
	}
	var rows []string
	if err := n.DB.RPC(ctx, "apply_auto_note_tags", map[string]any{
		"p_user_id":   userID,
		"p_note_id":   noteID,
		"p_auto_tags": normalizeTagList(cleaned),
	}, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// SetLockedTagsForNote replaces tags as locked manual/hashtag tags.
func (n *Notes) SetLockedTagsForNote(ctx context.Context, userID, noteID string, tags []string, origin string) ([]string, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, fmt.Errorf("notes disabled")
	}
	if origin != "hashtag" {
		origin = "manual"
	}
	var rows []string
	if err := n.DB.RPC(ctx, "set_locked_note_tags", map[string]any{
		"p_user_id": userID,
		"p_note_id": noteID,
		"p_tags":    normalizeTagList(tags),
		"p_origin":  origin,
	}, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetNoteTagDetails returns tags with origin/locked.
func (n *Notes) GetNoteTagDetails(ctx context.Context, userID, noteID string) ([]NoteTagDetail, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", "tag,origin,locked")
	q.Set("note_id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)
	q.Set("order", "tag.asc")

	var rows []NoteTagDetail
	if err := n.DB.Get(ctx, "note_tags", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// PinTag sets pinned on a taxonomy tag.
func (n *Notes) PinTag(ctx context.Context, userID, tag string, pinned bool) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	var out any
	return n.DB.RPC(ctx, "pin_tag", map[string]any{
		"p_user_id": userID,
		"p_tag":     NormalizeTagUnicode(tag),
		"p_pinned":  pinned,
	}, &out)
}

// AliasTag points source at canonical.
func (n *Notes) AliasTag(ctx context.Context, userID, source, canonical string) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	var out any
	return n.DB.RPC(ctx, "alias_tag", map[string]any{
		"p_user_id":    userID,
		"p_source":     NormalizeTagUnicode(source),
		"p_canonical":  NormalizeTagUnicode(canonical),
	}, &out)
}

// RenameTag renames a canonical tag.
func (n *Notes) RenameTag(ctx context.Context, userID, from, to string) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	var out any
	return n.DB.RPC(ctx, "rename_tag", map[string]any{
		"p_user_id": userID,
		"p_from":    NormalizeTagUnicode(from),
		"p_to":      NormalizeTagUnicode(to),
	}, &out)
}

// MergeTags merges source into canonical.
func (n *Notes) MergeTags(ctx context.Context, userID, source, canonical string) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	var out any
	return n.DB.RPC(ctx, "merge_tags", map[string]any{
		"p_user_id":   userID,
		"p_source":    NormalizeTagUnicode(source),
		"p_canonical": NormalizeTagUnicode(canonical),
	}, &out)
}

// MarkEnrichmentRunning sets enrichment_status=running.
func (n *Notes) MarkEnrichmentRunning(ctx context.Context, userID, noteID string) error {
	return n.patchEnrichment(ctx, userID, noteID, map[string]any{
		"enrichment_status": "running",
	})
}

// MarkEnrichmentSucceeded sets enrichment_status=succeeded and enrichment_version.
func (n *Notes) MarkEnrichmentSucceeded(ctx context.Context, userID, noteID string, version int64) error {
	return n.patchEnrichment(ctx, userID, noteID, map[string]any{
		"enrichment_status":  "succeeded",
		"enrichment_version": version,
	})
}

// MarkEnrichmentFailed sets enrichment_status=failed.
func (n *Notes) MarkEnrichmentFailed(ctx context.Context, userID, noteID string) error {
	return n.patchEnrichment(ctx, userID, noteID, map[string]any{
		"enrichment_status": "failed",
	})
}

func (n *Notes) patchEnrichment(ctx context.Context, userID, noteID string, body map[string]any) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	q := url.Values{}
	q.Set("id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)
	return n.DB.Patch(ctx, "notes", q, body)
}
