package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultFxTwitterBase   = "https://api.fxtwitter.com"
	defaultTwitterOEmbed   = "https://publish.twitter.com/oembed"
	tweetHTTPTimeout       = 12 * time.Second
	maxTweetAPIBytes       = 1_000_000
	twitterExtractor       = "twitter"
	twitterOEmbedExtractor = "twitter_oembed"
)

var (
	httpClient     = &http.Client{Timeout: 15 * time.Second}
	fxTwitterBase  = defaultFxTwitterBase
	twitterOEmbed  = defaultTwitterOEmbed
	tweetURLRegexp = regexp.MustCompile(`(?i)https?://(?:(?:www|mobile)\.)?(?:twitter\.com|x\.com|vxtwitter\.com|fxtwitter\.com|fixupx\.com)/(?:[A-Za-z0-9_]+|i(?:/web)?)/status(?:es)?/(\d+)`)
	tweetTestMu    sync.Mutex
)

type fxTweetResponse struct {
	Code    int     `json:"code"`
	Message string  `json:"message"`
	Tweet   fxTweet `json:"tweet"`
}

type fxTweet struct {
	ID               string   `json:"id"`
	URL              string   `json:"url"`
	Text             string   `json:"text"`
	CreatedAt        string   `json:"created_at"`
	CreatedTimestamp int64    `json:"created_timestamp"`
	Author           fxAuthor `json:"author"`
	Quote            *fxTweet `json:"quote"`
	Retweet          *fxTweet `json:"retweet"`
	ReplyingTo       string   `json:"replying_to"`
	Media            *fxMedia `json:"media"`
}

type fxAuthor struct {
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

type fxMedia struct {
	Photos []fxPhoto `json:"photos"`
	Videos []fxVideo `json:"videos"`
}

type fxPhoto struct {
	URL string `json:"url"`
}

type fxVideo struct {
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail_url"`
}

type twitterOEmbedResponse struct {
	HTML       string `json:"html"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_url"`
	URL        string `json:"url"`
}

// ParseTweetID returns the numeric status id if rawURL is a Twitter/X status link.
func ParseTweetID(rawURL string) (string, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", false
	}
	m := tweetURLRegexp.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}

// IsTweetURL reports whether rawURL points at a tweet/status page.
func IsTweetURL(rawURL string) bool {
	_, ok := ParseTweetID(rawURL)
	return ok
}

// FindTweetURLs returns unique Twitter/X status URLs in text, in appearance order.
func FindTweetURLs(text string) []string {
	matches := tweetURLRegexp.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, raw := range matches {
		cleaned := trimTrailingPunct(raw)
		id, ok := ParseTweetID(cleaned)
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

// ExtractTweet fetches a tweet by status URL using FxTwitter, falling back to oEmbed.
func ExtractTweet(rawURL string) (ExtractedAsset, error) {
	id, ok := ParseTweetID(rawURL)
	if !ok {
		return ExtractedAsset{}, fmt.Errorf("not a tweet URL")
	}
	if asset, err := extractTweetFx(id, rawURL); err == nil {
		return asset, nil
	} else if asset, oerr := extractTweetOEmbed(rawURL); oerr == nil {
		return asset, nil
	} else {
		return ExtractedAsset{}, fmt.Errorf("failed to fetch tweet %s: %v", id, err)
	}
}

// ExpandTweetLinks expands Twitter/X URLs. All http(s) links are expanded;
// tweets still use the Twitter extractor via ExtractURL.
func ExpandTweetLinks(content string) string {
	return ExpandLinks(content)
}

func extractTweetFx(id, originalURL string) (ExtractedAsset, error) {
	apiURL := strings.TrimRight(fxTwitterBase, "/") + "/status/" + id
	ctx, cancel := context.WithTimeout(context.Background(), tweetHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return ExtractedAsset{}, err
	}
	req.Header.Set("User-Agent", "DonnaKnowledgeBot/1.0")
	req.Header.Set("Accept", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return ExtractedAsset{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxTweetAPIBytes+1))
	if err != nil {
		return ExtractedAsset{}, err
	}
	if len(body) > maxTweetAPIBytes {
		return ExtractedAsset{}, fmt.Errorf("tweet API response too large")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ExtractedAsset{}, fmt.Errorf("tweet API status %d", res.StatusCode)
	}

	var parsed fxTweetResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ExtractedAsset{}, err
	}
	if parsed.Code != 0 && parsed.Code != 200 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = fmt.Sprintf("tweet API code %d", parsed.Code)
		}
		return ExtractedAsset{}, fmt.Errorf("%s", msg)
	}
	if strings.TrimSpace(parsed.Tweet.Text) == "" && parsed.Tweet.Retweet == nil {
		return ExtractedAsset{}, fmt.Errorf("tweet API returned empty text")
	}

	content, title, canonical := formatFxTweet(parsed.Tweet, originalURL)
	return ExtractedAsset{
		Content:   ClampText(content),
		AssetKind: AssetLink,
		MimeType:  "text/plain",
		Extractor: twitterExtractor,
		Title:     title,
		SourceURL: canonical,
	}, nil
}

func extractTweetOEmbed(rawURL string) (ExtractedAsset, error) {
	u, err := url.Parse(twitterOEmbed)
	if err != nil {
		return ExtractedAsset{}, err
	}
	q := u.Query()
	q.Set("url", rawURL)
	q.Set("omit_script", "true")
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), tweetHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ExtractedAsset{}, err
	}
	req.Header.Set("User-Agent", "DonnaKnowledgeBot/1.0")
	req.Header.Set("Accept", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return ExtractedAsset{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxTweetAPIBytes+1))
	if err != nil {
		return ExtractedAsset{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ExtractedAsset{}, fmt.Errorf("oEmbed status %d", res.StatusCode)
	}

	var parsed twitterOEmbedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ExtractedAsset{}, err
	}
	text := strings.TrimSpace(HTMLToText(html.UnescapeString(parsed.HTML)))
	if text == "" {
		return ExtractedAsset{}, fmt.Errorf("oEmbed returned empty text")
	}

	author := strings.TrimSpace(parsed.AuthorName)
	canonical := strings.TrimSpace(parsed.URL)
	if canonical == "" {
		canonical = rawURL
	}
	title := "Tweet"
	if author != "" {
		title = "Tweet by " + author
	}

	var b strings.Builder
	b.WriteString("User saved a tweet to memory.\n\n")
	if author != "" {
		b.WriteString("Author: ")
		b.WriteString(author)
		b.WriteByte('\n')
	}
	b.WriteString("URL: ")
	b.WriteString(canonical)
	b.WriteString("\n\n")
	b.WriteString(text)

	return ExtractedAsset{
		Content:   ClampText(b.String()),
		AssetKind: AssetLink,
		MimeType:  "text/plain",
		Extractor: twitterOEmbedExtractor,
		Title:     title,
		SourceURL: canonical,
	}, nil
}

func formatFxTweet(tweet fxTweet, originalURL string) (content, title, canonical string) {
	if tweet.Retweet != nil && strings.TrimSpace(tweet.Retweet.Text) != "" {
		inner := *tweet.Retweet
		if strings.TrimSpace(tweet.Author.ScreenName) != "" {
			inner.Text = fmt.Sprintf("RT @%s: %s", inner.Author.ScreenName, inner.Text)
		}
		return formatFxTweet(inner, originalURL)
	}

	handle := strings.TrimSpace(tweet.Author.ScreenName)
	name := strings.TrimSpace(tweet.Author.Name)
	canonical = strings.TrimSpace(tweet.URL)
	if canonical == "" {
		canonical = strings.TrimSpace(originalURL)
	}
	if canonical == "" && tweet.ID != "" && handle != "" {
		canonical = fmt.Sprintf("https://x.com/%s/status/%s", handle, tweet.ID)
	}

	title = "Tweet"
	if handle != "" && name != "" {
		title = fmt.Sprintf("Tweet by %s (@%s)", name, handle)
	} else if handle != "" {
		title = "Tweet by @" + handle
	} else if name != "" {
		title = "Tweet by " + name
	}

	var b strings.Builder
	b.WriteString("User saved a tweet to memory.\n\n")
	if name != "" || handle != "" {
		b.WriteString("Author: ")
		if name != "" && handle != "" {
			b.WriteString(name)
			b.WriteString(" (@")
			b.WriteString(handle)
			b.WriteByte(')')
		} else if handle != "" {
			b.WriteByte('@')
			b.WriteString(handle)
		} else {
			b.WriteString(name)
		}
		b.WriteByte('\n')
	}
	if canonical != "" {
		b.WriteString("URL: ")
		b.WriteString(canonical)
		b.WriteByte('\n')
	}
	if posted := tweetPostedAt(tweet); posted != "" {
		b.WriteString("Posted: ")
		b.WriteString(posted)
		b.WriteByte('\n')
	}
	if reply := strings.TrimSpace(tweet.ReplyingTo); reply != "" {
		b.WriteString("Replying to: @")
		b.WriteString(strings.TrimPrefix(reply, "@"))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(tweet.Text))

	if tweet.Quote != nil && strings.TrimSpace(tweet.Quote.Text) != "" {
		qHandle := strings.TrimSpace(tweet.Quote.Author.ScreenName)
		b.WriteString("\n\nQuoted")
		if qHandle != "" {
			b.WriteString(" @")
			b.WriteString(qHandle)
		}
		b.WriteString(":\n")
		b.WriteString(strings.TrimSpace(tweet.Quote.Text))
	}

	if tweet.Media != nil {
		var media []string
		for _, p := range tweet.Media.Photos {
			if u := strings.TrimSpace(p.URL); u != "" {
				media = append(media, u)
			}
		}
		for _, v := range tweet.Media.Videos {
			if u := strings.TrimSpace(v.URL); u != "" {
				media = append(media, u)
			} else if u := strings.TrimSpace(v.Thumbnail); u != "" {
				media = append(media, u)
			}
		}
		if len(media) > 0 {
			b.WriteString("\n\nMedia:\n")
			for _, u := range media {
				b.WriteString("- ")
				b.WriteString(u)
				b.WriteByte('\n')
			}
		}
	}

	return strings.TrimSpace(b.String()), title, canonical
}

func tweetPostedAt(tweet fxTweet) string {
	if tweet.CreatedTimestamp > 0 {
		return time.Unix(tweet.CreatedTimestamp, 0).UTC().Format(time.RFC3339)
	}
	return strings.TrimSpace(tweet.CreatedAt)
}

func alreadyExpandedTweet(content, tweetURL string) bool {
	if !strings.Contains(content, "User saved a tweet to memory") {
		return false
	}
	id, ok := ParseTweetID(tweetURL)
	if !ok {
		return false
	}
	return strings.Contains(content, "/status/"+id)
}

func trimTrailingPunct(raw string) string {
	return strings.TrimRight(raw, ".,;:!?)")
}

// SetTweetEndpointsForTest points tweet fetches at test servers. Restore with the returned func.
func SetTweetEndpointsForTest(fxBase, oembedBase string, client *http.Client) func() {
	tweetTestMu.Lock()
	prevFx := fxTwitterBase
	prevOEmbed := twitterOEmbed
	prevClient := httpClient
	if strings.TrimSpace(fxBase) != "" {
		fxTwitterBase = strings.TrimRight(fxBase, "/")
	}
	if strings.TrimSpace(oembedBase) != "" {
		twitterOEmbed = oembedBase
	}
	if client != nil {
		httpClient = client
	}
	return func() {
		fxTwitterBase = prevFx
		twitterOEmbed = prevOEmbed
		httpClient = prevClient
		tweetTestMu.Unlock()
	}
}
