package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Supabase struct {
	URL            string
	ServiceRoleKey string
	Client         *http.Client
}

func NewSupabase(supabaseURL, serviceRoleKey string) *Supabase {
	return &Supabase{
		URL:            strings.TrimSuffix(supabaseURL, "/"),
		ServiceRoleKey: serviceRoleKey,
		Client:         &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *Supabase) Enabled() bool {
	return s.URL != "" && s.ServiceRoleKey != ""
}

func (s *Supabase) restURL(table string, query url.Values) string {
	u := s.URL + "/rest/v1/" + table
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (s *Supabase) headers(prefer string) http.Header {
	h := http.Header{}
	h.Set("apikey", s.ServiceRoleKey)
	h.Set("Authorization", "Bearer "+s.ServiceRoleKey)
	h.Set("Content-Type", "application/json")
	if prefer != "" {
		h.Set("Prefer", prefer)
	}
	return h
}

func (s *Supabase) Get(ctx context.Context, table string, query url.Values, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.restURL(table, query), nil)
	if err != nil {
		return err
	}
	req.Header = s.headers("")

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("supabase GET %s %d: %s", table, res.StatusCode, string(b))
	}

	return json.NewDecoder(res.Body).Decode(dest)
}

func (s *Supabase) Insert(ctx context.Context, table string, body any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.restURL(table, nil), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = s.headers("return=representation")

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("supabase INSERT %s %d: %s", table, res.StatusCode, string(raw))
	}

	if dest != nil {
		return json.NewDecoder(res.Body).Decode(dest)
	}
	return nil
}

func (s *Supabase) Patch(ctx context.Context, table string, query url.Values, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, s.restURL(table, query), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = s.headers("")

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("supabase PATCH %s %d: %s", table, res.StatusCode, string(raw))
	}
	return nil
}

func (s *Supabase) Upsert(ctx context.Context, table, onConflict string, body any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	q := url.Values{}
	q.Set("on_conflict", onConflict)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.restURL(table, q), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = s.headers("return=representation,resolution=merge-duplicates")

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("supabase UPSERT %s %d: %s", table, res.StatusCode, string(raw))
	}

	if dest != nil {
		return json.NewDecoder(res.Body).Decode(dest)
	}
	return nil
}

func (s *Supabase) Count(ctx context.Context, table string, query url.Values) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.restURL(table, query), nil)
	if err != nil {
		return 0, err
	}
	req.Header = s.headers("count=exact")
	req.Header.Set("Range-Unit", "items")
	req.Header.Set("Range", "0-0")

	res, err := s.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return 0, fmt.Errorf("supabase COUNT %s %d: %s", table, res.StatusCode, string(raw))
	}

	contentRange := res.Header.Get("Content-Range")
	if contentRange == "" {
		return 0, nil
	}
	parts := strings.Split(contentRange, "/")
	if len(parts) != 2 {
		return 0, nil
	}
	var count int
	fmt.Sscanf(parts[1], "%d", &count)
	return count, nil
}

func (s *Supabase) UploadStorage(ctx context.Context, bucket, path, contentType string, data []byte) error {
	objectPath := url.PathEscape(path)
	objectPath = strings.ReplaceAll(objectPath, "%2F", "/")
	u := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.URL, bucket, objectPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.ServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)
	req.Header.Set("Content-Type", contentType)

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("storage upload %s %d: %s", path, res.StatusCode, string(raw))
	}
	return nil
}
