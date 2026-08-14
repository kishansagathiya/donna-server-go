package ingest

type AssetKind string

const (
	AssetDocument AssetKind = "document"
	AssetLink     AssetKind = "link"
	AssetImage    AssetKind = "image"
	AssetAudio    AssetKind = "audio"
	AssetText     AssetKind = "text"
)

type ExtractedAsset struct {
	Content   string
	AssetKind AssetKind
	MimeType  string
	Extractor string
	Title     string
	SourceURL string
}

type ExtractContext struct {
	Buffer   []byte
	Mime     string
	Filename string
}

type Extractor struct {
	Name      string
	Priority  int
	CanHandle func(mime, filename string) bool
	Extract   func(ctx ExtractContext) (ExtractedAsset, error)
}
