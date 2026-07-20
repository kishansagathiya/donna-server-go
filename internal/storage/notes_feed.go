package storage

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// NotesFeedQuery configures GET /notes/feed.
type NotesFeedQuery struct {
	Limit   int
	Cursor  string
	Query   string // optional FTS search
	Tag     string // optional tag filter
	Curated bool   // when true, hide voice_turn notes
}

// TagFacet is a canonical tag count returned with the feed.
type TagFacet struct {
	Tag       string  `json:"tag"`
	Canonical string  `json:"canonical"`
	Count     int     `json:"count"`
	Pinned    bool    `json:"pinned"`
	AliasOf   *string `json:"alias_of,omitempty"`
}

// NotesFeed is the cursor-paginated notes feed response.
type NotesFeed struct {
	Items      []NoteSummary `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Facets     NotesFacets   `json:"facets"`
}

// NotesFacets holds feed facet payloads.
type NotesFacets struct {
	Tags []TagFacet `json:"tags"`
}

type feedCursor struct {
	NoteDate string
	ID       string
}

func encodeFeedCursor(noteDate, id string) string {
	raw := noteDate + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeFeedCursor(raw string) (feedCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return feedCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return feedCursor{}, fmt.Errorf("invalid_cursor")
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return feedCursor{}, fmt.Errorf("invalid_cursor")
	}
	return feedCursor{NoteDate: parts[0], ID: parts[1]}, nil
}

// ListFeed returns a cursor page of notes with tag facets and enrichment state.
func (n *Notes) ListFeed(ctx context.Context, userID string, in NotesFeedQuery) (NotesFeed, error) {
	out := NotesFeed{Items: []NoteSummary{}, Facets: NotesFacets{Tags: []TagFacet{}}}
	if n == nil || !n.Enabled {
		return out, fmt.Errorf("notes disabled")
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	cur, err := decodeFeedCursor(in.Cursor)
	if err != nil {
		return out, err
	}

	var noteIDs []string
	tag := strings.ToLower(strings.TrimSpace(in.Tag))
	if tag != "" {
		noteIDs, err = n.ListNoteIDsForTag(ctx, userID, tag)
		if err != nil {
			return out, err
		}
		if len(noteIDs) == 0 {
			facets, ferr := n.ListTagFacets(ctx, userID, 30)
			if ferr != nil {
				return out, ferr
			}
			out.Facets.Tags = facets
			return out, nil
		}
	}

	q := url.Values{}
	q.Set("select", n.summaryColumns())
	q.Set("user_id", "eq."+userID)
	q.Set("order", "note_date.desc,id.desc")
	q.Set("limit", fmt.Sprintf("%d", limit+1)) // one extra to detect next page

	if in.Curated {
		q.Set("source_type", "neq.voice_turn")
	}
	if search := strings.TrimSpace(in.Query); search != "" {
		terms := extractNoteSearchTerms(search)
		if len(terms) > 0 {
			q.Set("search_vector", "fts.english.websearch."+strings.Join(terms, " | "))
		}
	}
	if len(noteIDs) > 0 {
		q.Set("id", "in."+strings.Join(noteIDs, ","))
	}
	if cur.ID != "" {
		// (note_date, id) < (cursor) in descending order
		q.Set("or", fmt.Sprintf("(and(note_date.eq.%s,id.lt.%s),note_date.lt.%s)", cur.NoteDate, cur.ID, cur.NoteDate))
	}

	var raw []noteSummaryRow
	if err := n.DB.Get(ctx, "notes", q, &raw); err != nil {
		return out, err
	}

	hasMore := len(raw) > limit
	if hasMore {
		raw = raw[:limit]
	}

	items := make([]NoteSummary, 0, len(raw))
	ids := make([]string, 0, len(raw))
	for _, r := range raw {
		s := r.toSummary()
		items = append(items, s)
		ids = append(ids, s.ID)
	}

	tagMap, err := n.TagsForNotes(ctx, userID, ids)
	if err != nil {
		return out, err
	}
	for i := range items {
		if tags, ok := tagMap[items[i].ID]; ok {
			items[i].Tags = tags
		} else {
			items[i].Tags = []string{}
		}
	}

	out.Items = items
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		out.NextCursor = encodeFeedCursor(last.NoteDate, last.ID)
	}

	facets, err := n.ListTagFacets(ctx, userID, 30)
	if err != nil {
		return out, err
	}
	out.Facets.Tags = facets
	return out, nil
}

// ListTagFacets returns top tags with canonical naming fields for feed facets.
func (n *Notes) ListTagFacets(ctx context.Context, userID string, limit int) ([]TagFacet, error) {
	if n == nil || n.DB == nil || !n.Enabled {
		return []TagFacet{}, nil
	}
	if limit <= 0 {
		limit = 30
	}
	q := url.Values{}
	q.Set("select", "name,count,normalized_name,alias_of,pinned")
	q.Set("user_id", "eq."+userID)
	q.Set("order", "count.desc,name.asc")
	q.Set("limit", fmt.Sprintf("%d", limit))

	var rows []struct {
		Name           string  `json:"name"`
		Count          int     `json:"count"`
		NormalizedName *string `json:"normalized_name"`
		AliasOf        *string `json:"alias_of"`
		Pinned         bool    `json:"pinned"`
	}
	if err := n.DB.Get(ctx, "tags", q, &rows); err != nil {
		return nil, err
	}
	out := make([]TagFacet, 0, len(rows))
	for _, r := range rows {
		canonical := r.Name
		if r.NormalizedName != nil && strings.TrimSpace(*r.NormalizedName) != "" {
			canonical = strings.TrimSpace(*r.NormalizedName)
		}
		out = append(out, TagFacet{
			Tag:       r.Name,
			Canonical: canonical,
			Count:     r.Count,
			Pinned:    r.Pinned,
			AliasOf:   r.AliasOf,
		})
	}
	return out, nil
}
