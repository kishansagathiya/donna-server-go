package storage

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Tag is one entry in the per-user tag cloud.
type Tag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// NoteTags is the tag set on a single note.
type NoteTags struct {
	NoteID string   `json:"note_id"`
	Tags   []string `json:"tags"`
}

// SetTagsForNote replaces a note's tag set transactionally via the set_note_tags RPC,
// keeping the tags registry count consistent. Returns the final tag list.
func (n *Notes) SetTagsForNote(ctx context.Context, userID, noteID string, tags []string) ([]string, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, fmt.Errorf("notes disabled")
	}
	var rows []string
	if err := n.DB.RPC(ctx, "set_note_tags", map[string]any{
		"p_user_id": userID,
		"p_note_id": noteID,
		"p_tags":    normalizeTagList(tags),
	}, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetTagsForNote returns the tags on a single note.
func (n *Notes) GetTagsForNote(ctx context.Context, userID, noteID string) ([]string, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", "tag")
	q.Set("note_id", "eq."+noteID)
	q.Set("user_id", "eq."+userID)
	q.Set("order", "tag.asc")

	var rows []struct {
		Tag string `json:"tag"`
	}
	if err := n.DB.Get(ctx, "note_tags", q, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Tag)
	}
	return out, nil
}

// ListTopTags returns the most-used tags for the user.
func (n *Notes) ListTopTags(ctx context.Context, userID string, limit int) ([]Tag, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", "name,count")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "count.desc,name.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []Tag
	if err := n.DB.Get(ctx, "tags", q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ListNoteIDsForTag returns note IDs carrying the given tag.
func (n *Notes) ListNoteIDsForTag(ctx context.Context, userID, tag string) ([]string, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return nil, nil
	}
	q := url.Values{}
	q.Set("select", "note_id")
	q.Set("user_id", "eq."+userID)
	q.Set("tag", "eq."+strings.ToLower(strings.TrimSpace(tag)))
	q.Set("order", "created_at.desc")

	var rows []struct {
		NoteID string `json:"note_id"`
	}
	if err := n.DB.Get(ctx, "note_tags", q, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.NoteID)
	}
	return out, nil
}

// RecomputeTagCounts repairs the denormalized tags.count from note_tags.
func (n *Notes) RecomputeTagCounts(ctx context.Context, userID string) error {
	if n == nil || n.DB == nil || !n.Enabled {
		return fmt.Errorf("notes disabled")
	}
	var out any
	return n.DB.RPC(ctx, "recompute_tag_counts", map[string]any{
		"p_user_id": userID,
	}, &out)
}

// TagsForNotes returns a map of noteID -> tags for a batch of note IDs.
// Used to populate tag chips on list views in one round-trip.
func (n *Notes) TagsForNotes(ctx context.Context, userID string, noteIDs []string) (map[string][]string, error) {
	if n == nil || n.DB == nil || !n.Enabled || len(noteIDs) == 0 {
		return map[string][]string{}, nil
	}
	// PostgREST "in" filter: note_id=in.a,b,c
	q := url.Values{}
	q.Set("select", "note_id,tag")
	q.Set("user_id", "eq."+userID)
	q.Set("note_id", "in.("+strings.Join(noteIDs, ",")+")")
	q.Set("order", "tag.asc")

	var rows []struct {
		NoteID string `json:"note_id"`
		Tag    string `json:"tag"`
	}
	if err := n.DB.Get(ctx, "note_tags", q, &rows); err != nil {
		return nil, err
	}
	out := make(map[string][]string)
	for _, r := range rows {
		out[r.NoteID] = append(out[r.NoteID], r.Tag)
	}
	return out, nil
}

func normalizeTagList(tags []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// hashtagPattern matches inline #tags in note content (Steve's extractor).
// Allows letters, digits, underscore, and hyphen; must be preceded by start
// or whitespace so URLs (foo.com/#bar) don't get parsed as tags.
var hashtagPattern = regexp.MustCompile(`(?:^|\s)#([A-Za-z0-9][A-Za-z0-9_-]{0,40})`)

// ExtractHashtags returns inline #tags from the text, lowercased + deduped.
func ExtractHashtags(text string) []string {
	matches := hashtagPattern.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		tag := strings.ToLower(strings.TrimSpace(m[1]))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}