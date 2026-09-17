package library

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Sidecar file names understood in a book's folder, in the format
// Audiobookshelf (ABS) writes/reads them.
const (
	sidecarJSONName   = "metadata.json"
	sidecarTextName   = "metadata.abs"
	sidecarDescName   = "desc.txt"
	sidecarReaderName = "reader.txt"
)

// sidecarMeta holds book metadata parsed from ABS sidecar files, to be
// overlaid onto tag-derived values (sidecar wins over tags when non-empty).
type sidecarMeta struct {
	title         string
	subtitle      string
	authors       []string
	narrators     []string
	series        string
	seriesSeq     string
	description   string
	publishedYear *int64
	language      string
	asin          string
	isbn          string
	chapters      []chapterRow // non-empty means "replace ffprobe/synthesized chapters"
}

// sidecarFile records stat info for a file that influences derived
// metadata but isn't an audio file itself, so its name/size/mtime must be
// folded into the scan hash for changes to be picked up on rescan.
type sidecarFile struct {
	name    string
	size    int64
	mtimeMs int64
}

// sidecarFilesPresent stat's a book folder's sidecar files (without
// reading/parsing them) so their name/size/mtime can be folded into the
// scan hash cheaply, before deciding whether the (expensive) tag read is
// needed at all. Only applies to folder books.
func sidecarFilesPresent(bookDirAbs string, isSingleFile bool) []sidecarFile {
	if isSingleFile {
		return nil
	}
	var files []sidecarFile
	for _, name := range []string{sidecarJSONName, sidecarTextName, sidecarDescName, sidecarReaderName} {
		fi, err := os.Stat(filepath.Join(bookDirAbs, name))
		if err != nil || fi.IsDir() {
			continue
		}
		files = append(files, sidecarFile{name: name, size: fi.Size(), mtimeMs: fi.ModTime().UnixMilli()})
	}
	return files
}

// applySidecar reads ABS sidecar metadata from a book's folder, if any is
// present, and overlays non-empty values onto meta (sidecar wins over
// tags). metadata.json wins over metadata.abs when both exist. desc.txt/
// reader.txt are only consulted when description/narrators are still empty
// after tags and metadata.json/.abs. It returns the sidecar files found
// (for the scan hash) and any sidecar-provided chapters (which replace
// ffprobe/synthesized chapters when non-empty). Only applies to folder
// books; a single-file book has no folder of its own to search.
func applySidecar(meta *bookMeta, bookDirAbs string, isSingleFile bool) ([]sidecarFile, []chapterRow) {
	if isSingleFile {
		return nil, nil
	}

	files := sidecarFilesPresent(bookDirAbs, isSingleFile)
	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.name] = true
	}
	read := func(name string) ([]byte, bool) {
		data, err := os.ReadFile(filepath.Join(bookDirAbs, name))
		return data, err == nil
	}

	var sc sidecarMeta
	switch {
	case present[sidecarJSONName]:
		if data, ok := read(sidecarJSONName); ok {
			parseJSONSidecar(data, &sc)
		}
	case present[sidecarTextName]:
		if data, ok := read(sidecarTextName); ok {
			parseTextSidecar(data, &sc)
		}
	}

	applySidecarOverrides(meta, sc)

	if meta.description == "" && present[sidecarDescName] {
		if data, ok := read(sidecarDescName); ok {
			if s := strings.TrimSpace(string(data)); s != "" {
				meta.description = s
			}
		}
	}
	if (meta.narratorsJSON == "" || meta.narratorsJSON == "[]") && present[sidecarReaderName] {
		if data, ok := read(sidecarReaderName); ok {
			if s := strings.TrimSpace(string(data)); s != "" {
				meta.narratorsJSON = namesJSON(splitNames(s))
			}
		}
	}

	for i := range sc.chapters {
		if sc.chapters[i].Title == "" {
			sc.chapters[i].Title = fmt.Sprintf("Chapter %d", i+1)
		}
	}

	return files, sc.chapters
}

// applySidecarOverrides overlays non-empty sidecar-derived fields onto
// tag-derived book metadata.
func applySidecarOverrides(meta *bookMeta, sc sidecarMeta) {
	if sc.title != "" {
		meta.title = sc.title
	}
	if sc.subtitle != "" {
		meta.subtitle = sc.subtitle
	}
	if len(sc.authors) > 0 {
		meta.authorsJSON = namesJSON(sc.authors)
	}
	if len(sc.narrators) > 0 {
		meta.narratorsJSON = namesJSON(sc.narrators)
	}
	if sc.series != "" {
		meta.series = sc.series
		meta.seriesSeq = sc.seriesSeq
	}
	if sc.description != "" {
		meta.description = sc.description
	}
	if sc.publishedYear != nil {
		meta.publishedYear = sc.publishedYear
	}
	if sc.language != "" {
		meta.language = sc.language
	}
	if sc.asin != "" {
		meta.asin = sc.asin
	}
	if sc.isbn != "" {
		meta.isbn = sc.isbn
	}
}

// absSidecarJSON mirrors Audiobookshelf's metadata.json layout. All fields
// are optional and may be null.
type absSidecarJSON struct {
	Title         *string          `json:"title"`
	Subtitle      *string          `json:"subtitle"`
	Authors       []string         `json:"authors"`
	Narrators     []string         `json:"narrators"`
	Series        []string         `json:"series"`
	PublishedYear json.RawMessage  `json:"publishedYear"`
	Description   *string          `json:"description"`
	ISBN          *string          `json:"isbn"`
	ASIN          *string          `json:"asin"`
	Language      *string          `json:"language"`
	Chapters      []absChapterJSON `json:"chapters"`
}

type absChapterJSON struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

// parseJSONSidecar parses ABS's metadata.json into sc, leaving fields not
// present (or null/empty) untouched.
func parseJSONSidecar(data []byte, sc *sidecarMeta) {
	var raw absSidecarJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	if raw.Title != nil {
		sc.title = strings.TrimSpace(*raw.Title)
	}
	if raw.Subtitle != nil {
		sc.subtitle = strings.TrimSpace(*raw.Subtitle)
	}
	if len(raw.Authors) > 0 {
		sc.authors = trimNonEmpty(raw.Authors)
	}
	if len(raw.Narrators) > 0 {
		sc.narrators = trimNonEmpty(raw.Narrators)
	}
	if len(raw.Series) > 0 && strings.TrimSpace(raw.Series[0]) != "" {
		sc.series, sc.seriesSeq = splitSeriesEntry(raw.Series[0])
	}
	if raw.Description != nil {
		sc.description = strings.TrimSpace(*raw.Description)
	}
	if raw.ISBN != nil {
		sc.isbn = strings.TrimSpace(*raw.ISBN)
	}
	if raw.ASIN != nil {
		sc.asin = strings.TrimSpace(*raw.ASIN)
	}
	if raw.Language != nil {
		sc.language = strings.TrimSpace(*raw.Language)
	}
	if y := parsePublishedYear(raw.PublishedYear); y != nil {
		sc.publishedYear = y
	}
	if len(raw.Chapters) > 0 {
		chs := make([]chapterRow, 0, len(raw.Chapters))
		for _, c := range raw.Chapters {
			chs = append(chs, chapterRow{
				Title:   strings.TrimSpace(c.Title),
				StartMs: int64(math.Round(c.Start * 1000)),
				EndMs:   int64(math.Round(c.End * 1000)),
			})
		}
		sc.chapters = chs
	}
}

// absTextChapter accumulates one [CHAPTER] block from a metadata.abs file.
type absTextChapter struct {
	start, end       float64
	hasStart, hasEnd bool
	title            string
}

// parseTextSidecar best-effort parses the older plain-text metadata.abs
// format: "key=value" lines using the same keys as metadata.json (list
// values comma-separated), plus optional [CHAPTER] blocks with start=/
// end=/title= lines (seconds). Unknown keys are ignored.
func parseTextSidecar(data []byte, sc *sidecarMeta) {
	lines := strings.Split(string(data), "\n")

	var chapters []chapterRow
	var cur *absTextChapter
	flush := func() {
		if cur != nil && cur.hasStart && cur.hasEnd {
			chapters = append(chapters, chapterRow{
				Title:   cur.title,
				StartMs: int64(math.Round(cur.start * 1000)),
				EndMs:   int64(math.Round(cur.end * 1000)),
			})
		}
		cur = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "[CHAPTER]") {
			flush()
			cur = &absTextChapter{}
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])

		if cur != nil {
			switch key {
			case "start":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					cur.start = f
					cur.hasStart = true
				}
			case "end":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					cur.end = f
					cur.hasEnd = true
				}
			case "title":
				cur.title = val
			}
			continue
		}

		applyTextSidecarKey(sc, key, val)
	}
	flush()

	if len(chapters) > 0 {
		sc.chapters = chapters
	}
}

func applyTextSidecarKey(sc *sidecarMeta, key, val string) {
	if val == "" {
		return
	}
	switch key {
	case "title":
		sc.title = val
	case "subtitle":
		sc.subtitle = val
	case "authors":
		sc.authors = trimNonEmpty(strings.Split(val, ","))
	case "narrators":
		sc.narrators = trimNonEmpty(strings.Split(val, ","))
	case "series":
		sc.series, sc.seriesSeq = splitSeriesEntry(val)
	case "description":
		sc.description = val
	case "publishedyear":
		if y := yearFromString(val); y != nil {
			sc.publishedYear = y
		}
	case "isbn":
		sc.isbn = val
	case "asin":
		sc.asin = val
	case "language":
		sc.language = val
	}
}

// splitSeriesEntry splits an ABS series entry ("Name #seq") on the LAST
// " #", since a series name may itself contain "#". seq is returned
// verbatim (it may be "1", "1.5", or "Book 1").
func splitSeriesEntry(s string) (name, seq string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, " #"); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+2:])
	}
	return s, ""
}

// trimNonEmpty trims whitespace from each string and drops empty results,
// returning nil if nothing remains.
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parsePublishedYear handles ABS's publishedYear field, which may be a
// JSON string, a JSON number, or null/absent.
func parsePublishedYear(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return yearFromString(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return yearFromString(n.String())
	}
	return nil
}

// yearFromString extracts the first 4 consecutive digits from s as a year.
func yearFromString(s string) *int64 {
	m := yearRe.FindString(s)
	if m == "" {
		return nil
	}
	y, err := strconv.ParseInt(m, 10, 64)
	if err != nil {
		return nil
	}
	return &y
}
