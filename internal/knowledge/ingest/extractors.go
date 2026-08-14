package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/providers"
)

type Services struct {
	STT *providers.STT
	LLM *providers.LLM
}

const (
	visionCallTimeout     = 60 * time.Second
	knowledgeVisionPrompt = "Describe this image in detail for a personal knowledge base. Include all visible text (OCR), people, objects, diagrams, and key information. Be factual and thorough."
	// Chat attachments are ephemeral. Keep this short so 10-image turns
	// do not spend minutes generating knowledge-base essays before the reply.
	ChatVisionPrompt = "Describe this image for a chat assistant answering the user now. Include all visible text (OCR) and the key people, objects, and layout. Be factual and concise."
)

var (
	fileExtractors []Extractor
	visionLLM      *providers.LLM
)

func InitExtractors(services Services) {
	visionLLM = services.LLM
	fileExtractors = []Extractor{
		{
			Name: "plain_text", Priority: 10,
			CanHandle: func(mime, _ string) bool {
				return (strings.HasPrefix(mime, "text/") && mime != "text/html" && mime != "text/csv") ||
					mime == "application/xml" || mime == "text/xml"
			},
			Extract: func(ctx ExtractContext) (ExtractedAsset, error) {
				return ExtractedAsset{
					Content: ClampText(string(ctx.Buffer)), AssetKind: AssetKindFromMime(ctx.Mime),
					MimeType: ctx.Mime, Extractor: "plain_text", Title: ctx.Filename,
				}, nil
			},
		},
		{
			Name: "html_strip", Priority: 20,
			CanHandle: func(mime, _ string) bool { return mime == "text/html" },
			Extract: func(ctx ExtractContext) (ExtractedAsset, error) {
				return ExtractedAsset{
					Content: ClampText(HTMLToText(string(ctx.Buffer))), AssetKind: AssetDocument,
					MimeType: ctx.Mime, Extractor: "html_strip", Title: ctx.Filename,
				}, nil
			},
		},
		{
			Name: "structured_data", Priority: 25,
			CanHandle: func(mime, _ string) bool { return mime == "application/json" || mime == "text/csv" },
			Extract: func(ctx ExtractContext) (ExtractedAsset, error) {
				var content string
				if ctx.Mime == "application/json" {
					var parsed any
					if err := json.Unmarshal(ctx.Buffer, &parsed); err != nil {
						return ExtractedAsset{}, err
					}
					b, _ := json.MarshalIndent(parsed, "", "  ")
					content = fmt.Sprintf("JSON document (%s):\n%s", defaultName(ctx.Filename, "data.json"), string(b))
				} else {
					content = fmt.Sprintf("CSV document (%s):\n%s", defaultName(ctx.Filename, "data.csv"), string(ctx.Buffer))
				}
				return ExtractedAsset{
					Content: ClampText(content), AssetKind: AssetDocument,
					MimeType: ctx.Mime, Extractor: "structured_data", Title: ctx.Filename,
				}, nil
			},
		},
		{
			Name: "pdf_parse", Priority: 30,
			CanHandle: func(mime, _ string) bool { return mime == "application/pdf" },
			Extract:   extractPDF,
		},
		{
			Name: "mammoth_docx", Priority: 35,
			CanHandle: func(mime, _ string) bool {
				return mime == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			},
			Extract: extractDOCX,
		},
		{
			Name: "vision_llm", Priority: 40,
			CanHandle: func(mime, _ string) bool { return strings.HasPrefix(mime, "image/") },
			Extract: func(ctx ExtractContext) (ExtractedAsset, error) {
				dataURL := fmt.Sprintf("data:%s;base64,%s", ctx.Mime, base64.StdEncoding.EncodeToString(ctx.Buffer))
				desc, err := DescribeImage(ctx.Ctx, knowledgeVisionPrompt, dataURL)
				if err != nil {
					return ExtractedAsset{}, err
				}
				content := ClampText(fmt.Sprintf("Image: %s\n\n%s", defaultName(ctx.Filename, "upload"), desc))
				return ExtractedAsset{
					Content: content, AssetKind: AssetImage,
					MimeType: ctx.Mime, Extractor: "vision_llm", Title: ctx.Filename,
				}, nil
			},
		},
		{
			Name: "stt_transcribe", Priority: 50,
			CanHandle: func(mime, _ string) bool { return strings.HasPrefix(mime, "audio/") },
			Extract: func(ctx ExtractContext) (ExtractedAsset, error) {
				format := audioFormat(ctx.Mime)
				transcript, _, err := services.STT.TranscribeAudio(context.Background(), ctx.Buffer, format)
				if err != nil {
					return ExtractedAsset{}, err
				}
				text := strings.TrimSpace(transcript)
				if text == "" {
					return ExtractedAsset{}, fmt.Errorf("no speech detected in audio file")
				}
				content := ClampText(fmt.Sprintf("Audio note: %s\n\n%s", defaultName(ctx.Filename, "recording"), text))
				return ExtractedAsset{
					Content: content, AssetKind: AssetAudio,
					MimeType: ctx.Mime, Extractor: "stt_transcribe", Title: ctx.Filename,
				}, nil
			},
		},
	}
}

// DescribeImage runs the vision model. A 60s timeout is applied when ctx has none.
func DescribeImage(ctx context.Context, prompt, imageDataURL string) (string, error) {
	if visionLLM == nil {
		return "", fmt.Errorf("vision model is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, visionCallTimeout)
		defer cancel()
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = knowledgeVisionPrompt
	}
	return visionLLM.CompleteOnceVision(ctx, prompt, imageDataURL)
}

func DispatchFileExtraction(buffer []byte, contentType, filename string) (ExtractedAsset, error) {
	mime := ResolveMime(buffer, contentType, filename)
	ctx := ExtractContext{Buffer: buffer, Mime: mime, Filename: filename}

	var match *Extractor
	for i := range fileExtractors {
		e := &fileExtractors[i]
		if e.CanHandle(mime, filename) {
			if match == nil || e.Priority > match.Priority {
				match = e
			}
		}
	}
	if match == nil {
		return ExtractedAsset{}, fmt.Errorf("Unsupported file type: %s", mime)
	}
	return match.Extract(ctx)
}

func ExtractTextBody(text, title string) ExtractedAsset {
	return ExtractedAsset{
		Content: ClampText(strings.TrimSpace(text)), AssetKind: AssetText,
		MimeType: "text/plain", Extractor: "plain_text", Title: title,
	}
}

func ExtractURL(rawURL string) (ExtractedAsset, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ExtractedAsset{}, fmt.Errorf("only HTTP/HTTPS URLs are supported")
	}
	if err := rejectPrivateURL(rawURL); err != nil {
		return ExtractedAsset{}, err
	}
	if IsTweetURL(rawURL) {
		return ExtractTweet(rawURL)
	}

	fetched, fetchErr := fetchPublicPage(rawURL)
	if fetchErr == nil && IsTweetURL(fetched.SourceURL) {
		return ExtractTweet(fetched.SourceURL)
	}

	pageText := fetched.Content
	if i := strings.LastIndex(fetched.Content, "\n\n"); i >= 0 {
		pageText = fetched.Content[i+2:]
	}
	incomplete := fetchErr != nil || fetchLooksIncomplete(pageText, len(pageText), true)
	if incomplete && browseEnabled() && !IsTweetURL(rawURL) {
		if browsed, err := browsePage(rawURL); err == nil {
			return browsed, nil
		}
	}
	if fetchErr != nil {
		return ExtractedAsset{}, fetchErr
	}
	if strings.TrimSpace(pageText) == "" {
		return ExtractedAsset{}, fmt.Errorf("no text content extracted from URL")
	}
	return fetched, nil
}

func fetchPublicPage(rawURL string) (ExtractedAsset, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ExtractedAsset{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	req.Header.Set("User-Agent", "DonnaKnowledgeBot/1.0")

	res, err := httpClient.Do(req)
	if err != nil {
		return ExtractedAsset{}, err
	}
	defer res.Body.Close()

	finalURL := rawURL
	if res.Request != nil && res.Request.URL != nil {
		finalURL = res.Request.URL.String()
	}
	if IsTweetURL(finalURL) {
		return ExtractedAsset{SourceURL: finalURL}, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ExtractedAsset{}, fmt.Errorf("failed to fetch URL (%d)", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, MaxURLBytes+1))
	if err != nil {
		return ExtractedAsset{}, err
	}
	if len(body) > MaxURLBytes {
		return ExtractedAsset{}, fmt.Errorf("URL content too large")
	}

	contentType := res.Header.Get("Content-Type")
	isHTML := strings.Contains(contentType, "html") || strings.HasPrefix(strings.TrimSpace(string(body)), "<")
	content := ClampText(HTMLToText(string(body)))
	if !isHTML {
		content = ClampText(string(body))
	}

	host := parsed.Host
	if res.Request != nil && res.Request.URL != nil && res.Request.URL.Host != "" {
		host = res.Request.URL.Host
	}
	title := host
	return ExtractedAsset{
		Content:   fmt.Sprintf("# %s\nURL: %s\n\n%s", title, finalURL, content),
		AssetKind: AssetLink,
		MimeType:  "text/html",
		Extractor: "url_fetch",
		Title:     title,
		SourceURL: finalURL,
	}, nil
}

func extractPDF(ctx ExtractContext) (ExtractedAsset, error) {
	reader, err := pdf.NewReader(bytes.NewReader(ctx.Buffer), int64(len(ctx.Buffer)))
	if err != nil {
		return ExtractedAsset{}, err
	}
	var b strings.Builder
	pages := reader.NumPage()
	for i := 1; i <= pages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		b.WriteString(text)
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return ExtractedAsset{}, fmt.Errorf("no text extracted from PDF")
	}
	return ExtractedAsset{
		Content: ClampText(text), AssetKind: AssetDocument,
		MimeType: ctx.Mime, Extractor: "pdf_parse", Title: ctx.Filename,
	}, nil
}

func extractDOCX(ctx ExtractContext) (ExtractedAsset, error) {
	zr, err := zip.NewReader(bytes.NewReader(ctx.Buffer), int64(len(ctx.Buffer)))
	if err != nil {
		return ExtractedAsset{}, err
	}
	var xmlData []byte
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return ExtractedAsset{}, err
			}
			xmlData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return ExtractedAsset{}, err
			}
			break
		}
	}
	if len(xmlData) == 0 {
		return ExtractedAsset{}, fmt.Errorf("no text extracted from DOCX")
	}
	re := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matches := re.FindAllSubmatch(xmlData, -1)
	var parts []string
	for _, m := range matches {
		parts = append(parts, string(m[1]))
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text == "" {
		return ExtractedAsset{}, fmt.Errorf("no text extracted from DOCX")
	}
	return ExtractedAsset{
		Content: ClampText(text), AssetKind: AssetDocument,
		MimeType: ctx.Mime, Extractor: "mammoth_docx", Title: ctx.Filename,
	}, nil
}

func audioFormat(mime string) string {
	switch mime {
	case "audio/wav":
		return "wav"
	case "audio/mp4", "audio/x-m4a":
		return "m4a"
	default:
		return "mp3"
	}
}

func defaultName(filename, fallback string) string {
	if filename != "" {
		return filename
	}
	return fallback
}
