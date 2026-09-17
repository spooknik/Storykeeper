package api

// Wire types. docs/API.md and web/src/lib/api/types.ts mirror these exactly.

type Library struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"` // admin only
	BookCount  int    `json:"book_count"`
	Restricted bool   `json:"restricted"`
	CreatedAt  int64  `json:"created_at"`
}

type Progress struct {
	BookID     int64  `json:"book_id"`
	PositionMs int64  `json:"position_ms"`
	DurationMs int64  `json:"duration_ms"`
	FileIndex  int    `json:"file_index"`
	Seq        int64  `json:"seq"`
	ListenedAt int64  `json:"listened_at"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Finished   bool   `json:"finished"`
}

// ProgressReport is what a client sends. listened_at is derived server-side as
// server_now - (client_now - client_listened_at).
type ProgressReport struct {
	PositionMs       int64 `json:"position_ms"`
	FileIndex        int   `json:"file_index"`
	ClientListenedAt int64 `json:"client_listened_at"`
	ClientNow        int64 `json:"client_now"`
	BaseSeq          int64 `json:"base_seq"`
	Finished         *bool `json:"finished,omitempty"`
	// CSRFToken is only used by the sendBeacon alias, which cannot set headers.
	CSRFToken string `json:"csrf_token,omitempty"`
}

type BookSummary struct {
	ID         int64     `json:"id"`
	LibraryID  int64     `json:"library_id"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
	Authors    []string  `json:"authors"`
	Narrators  []string  `json:"narrators"`
	Series     string    `json:"series"`
	SeriesSeq  string    `json:"series_seq"`
	DurationMs int64     `json:"duration_ms"`
	CoverURL   string    `json:"cover_url"` // "" when no cover
	AddedAt    int64     `json:"added_at"`
	UpdatedAt  int64     `json:"updated_at"`
	Progress   *Progress `json:"progress,omitempty"`
}

type BookFile struct {
	Index      int    `json:"index"`
	RelPath    string `json:"rel_path"`
	Size       int64  `json:"size"`
	DurationMs int64  `json:"duration_ms"`
	Codec      string `json:"codec"`
	Bitrate    int    `json:"bitrate"`
	URL        string `json:"url"`
}

type Chapter struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
}

type Bookmark struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	PositionMs int64  `json:"position_ms"`
	Note       string `json:"note"`
	CreatedAt  int64  `json:"created_at"`
}

type BookDetail struct {
	BookSummary
	Description   string     `json:"description"`
	PublishedYear *int       `json:"published_year"`
	Language      string     `json:"language"`
	ASIN          string     `json:"asin"`
	ISBN          string     `json:"isbn"`
	Files         []BookFile `json:"files"`
	Chapters      []Chapter  `json:"chapters"`
	Bookmarks     []Bookmark `json:"bookmarks"`
}

type BookList struct {
	Items []BookSummary `json:"items"`
	Total int           `json:"total"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}
