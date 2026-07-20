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

func (s *Supabase) Delete(ctx context.Context, table string, query url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.restURL(table, query), nil)
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
		return fmt.Errorf("supabase DELETE %s %d: %s", table, res.StatusCode, string(raw))
	}
	return nil
}

func (s *Supabase) Patch(ctx context.Context, table string, query url.Values, body any) error {
	return s.patch(ctx, table, query, body, "", nil)
}

// PatchReturning PATCHes rows and decodes Prefer: return=representation into dest.
func (s *Supabase) PatchReturning(ctx context.Context, table string, query url.Values, body any, dest any) error {
	return s.patch(ctx, table, query, body, "return=representation", dest)
}

func (s *Supabase) patch(ctx context.Context, table string, query url.Values, body any, prefer string, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, s.restURL(table, query), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = s.headers(prefer)

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("supabase PATCH %s %d: %s", table, res.StatusCode, string(raw))
	}
	if dest != nil {
		return json.NewDecoder(res.Body).Decode(dest)
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

// RPC calls a PostgREST-callable function via POST /rest/v1/rpc/<function>
// and decodes the JSON response into dest.
func (s *Supabase) RPC(ctx context.Context, function string, body map[string]any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	u := s.restURL("rpc/"+function, nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
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
		return fmt.Errorf("supabase RPC %s %d: %s", function, res.StatusCode, string(raw))
	}

	return json.NewDecoder(res.Body).Decode(dest)
}

type storageListObject struct {
	Name string  `json:"name"`
	ID   *string `json:"id"`
}

func (s *Supabase) listStorageObjectsAtPrefix(ctx context.Context, bucket, prefix string) ([]storageListObject, error) {
	const pageSize = 1000
	var objects []storageListObject

	for offset := 0; ; offset += pageSize {
		body, err := json.Marshal(map[string]any{
			"prefix": prefix,
			"limit":  pageSize,
			"offset": offset,
		})
		if err != nil {
			return nil, err
		}

		u := fmt.Sprintf("%s/storage/v1/object/list/%s", s.URL, bucket)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("apikey", s.ServiceRoleKey)
		req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)
		req.Header.Set("Content-Type", "application/json")

		res, err := s.Client.Do(req)
		if err != nil {
			return nil, err
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return nil, fmt.Errorf("storage list %s %d: %s", bucket, res.StatusCode, string(raw))
		}

		var page []storageListObject
		if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
			res.Body.Close()
			return nil, err
		}
		res.Body.Close()

		if len(page) == 0 {
			break
		}

		objects = append(objects, page...)
		if len(page) < pageSize {
			break
		}
	}

	return objects, nil
}

func (s *Supabase) collectStorageObjectPaths(ctx context.Context, bucket, prefix string) ([]string, error) {
	entries, err := s.listStorageObjectsAtPrefix(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}

		fullPath := prefix + entry.Name
		if entry.ID == nil {
			childPaths, err := s.collectStorageObjectPaths(ctx, bucket, fullPath+"/")
			if err != nil {
				return nil, err
			}
			paths = append(paths, childPaths...)
			continue
		}

		paths = append(paths, fullPath)
	}

	return paths, nil
}

func (s *Supabase) ListStorageObjects(ctx context.Context, bucket, prefix string) ([]string, error) {
	return s.collectStorageObjectPaths(ctx, bucket, prefix)
}

func (s *Supabase) DeleteStorageObjects(ctx context.Context, bucket string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	const batchSize = 100
	for start := 0; start < len(paths); start += batchSize {
		end := start + batchSize
		if end > len(paths) {
			end = len(paths)
		}

		batch := paths[start:end]
		body, err := json.Marshal(batch)
		if err != nil {
			return err
		}

		u := fmt.Sprintf("%s/storage/v1/object/%s", s.URL, bucket)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("apikey", s.ServiceRoleKey)
		req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)
		req.Header.Set("Content-Type", "application/json")

		res, err := s.Client.Do(req)
		if err != nil {
			return err
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return fmt.Errorf("storage delete %s %d: %s", bucket, res.StatusCode, string(raw))
		}
		res.Body.Close()
	}

	return nil
}

func (s *Supabase) DeleteAuthUser(ctx context.Context, userID string) error {
	u := fmt.Sprintf("%s/auth/v1/admin/users/%s", s.URL, url.PathEscape(userID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", s.ServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)

	res, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("auth delete user %d: %s", res.StatusCode, string(raw))
	}
	return nil
}

func (s *Supabase) DownloadStorage(ctx context.Context, bucket, path string) ([]byte, error) {
	objectPath := url.PathEscape(path)
	objectPath = strings.ReplaceAll(objectPath, "%2F", "/")
	u := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.URL, bucket, objectPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", s.ServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)

	res, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("storage download %s %d: %s", path, res.StatusCode, string(raw))
	}
	return io.ReadAll(res.Body)
}

// StorageBucketURLOpts configures a generated storage URL.
type StorageBucketURLOpts struct {
	Expires time.Duration
}

// CreateSignedURL issues a Supabase Storage `sign` request for the given object
// and returns a fully-qualified, time-limited download URL. The bucket path is
// the object key (may contain `/`); it is path-escaped segment-wise. expiresIn
// is clamped to >=1s. Returns "" and no error if storage is unavailable, so
// callers can treat missing audio as a soft skip.
func (s *Supabase) CreateSignedURL(ctx context.Context, bucket, path string, expiresIn time.Duration) (string, error) {
	if !s.Enabled() {
		return "", nil
	}
	if expiresIn <= 0 {
		expiresIn = time.Minute
	}

	objectPath := url.PathEscape(path)
	objectPath = strings.ReplaceAll(objectPath, "%2F", "/")
	u := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", s.URL, bucket, objectPath)

	body, err := json.Marshal(map[string]any{"expiresIn": int(expiresIn.Seconds())})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("apikey", s.ServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+s.ServiceRoleKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("storage sign %s/%s %d: %s", bucket, path, res.StatusCode, string(raw))
	}

	var out struct {
		SignedURL string `json:"signedURL"`
		W_signed  string `json:"signedUrl"` // some Supabase versions return lower-camel
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	signed := out.SignedURL
	if signed == "" {
		signed = out.W_signed
	}
	if signed == "" {
		return "", fmt.Errorf("storage sign %s/%s: empty signedURL", bucket, path)
	}
	return resolveSignedStorageURL(s.URL, signed), nil
}

// resolveSignedStorageURL turns a Supabase storage sign response into a
// fully-qualified download URL. Recent Supabase versions return a relative path
// like "/object/sign/bucket/key?token=…" which must be served under /storage/v1.
func resolveSignedStorageURL(baseURL, signed string) string {
	if strings.HasPrefix(signed, "http://") || strings.HasPrefix(signed, "https://") {
		return signed
	}
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(signed, "/") {
		signed = "/" + signed
	}
	if strings.HasPrefix(signed, "/object/") && !strings.HasPrefix(signed, "/storage/v1/") {
		return base + "/storage/v1" + signed
	}
	return base + signed
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
