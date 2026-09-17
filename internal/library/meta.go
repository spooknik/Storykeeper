package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

// scanFormatVersion is folded into every book's scan hash. Bump it whenever
// scanner logic changes in a way that could change derived metadata for
// books whose on-disk files haven't changed, so a rescan re-reads and
// re-derives everything instead of skipping on a stale hash.
const scanFormatVersion = 2

// fileMeta holds everything known about one audio file within a book while
// it is being scanned.
type fileMeta struct {
	relPath string // relative to the book folder, forward-slash separated
	absPath string
	size    int64
	mtimeMs int64

	tags    map[string][]string
	durMs   int64
	codec   string
	bitrate int

	disc     int
	track    int
	hasTrack bool
}

// bookMeta is the metadata derived for a book from its first file in play
// order (plus aggregates like duration across all files).
type bookMeta struct {
	title         string
	subtitle      string
	authorsJSON   string
	narratorsJSON string
	series        string
	seriesSeq     string
	description   string
	publishedYear *int64
	language      string
	durationMs    int64
	asin          string
	isbn          string

	coverData []byte
	coverExt  string
}

func firstTag(tags map[string][]string, key string) string {
	if v, ok := tags[key]; ok && len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// parseLeadingInt extracts a leading integer from strings like "3", "03",
// or "3/12" (disc/track numbers are sometimes written as "n/total").
func parseLeadingInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "/-"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

var yearRe = regexp.MustCompile(`\d{4}`)

// splitNames splits an author/narrator tag value into individual names.
// It only splits on separators that unambiguously delimit a list: ";" and
// " / " always split; ", " only splits when it yields 3+ parts, since a
// single comma is often "Last, First" rather than a list separator.
func splitNames(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.Contains(s, ";") {
		return splitTrim(s, ";")
	}
	if strings.Contains(s, " / ") {
		return splitTrim(s, " / ")
	}
	if strings.Contains(s, ", ") {
		parts := splitTrim(s, ", ")
		if len(parts) >= 3 {
			return parts
		}
	}
	return []string{s}
}

func splitTrim(s, sep string) []string {
	raw := strings.Split(s, sep)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func namesJSON(names []string) string {
	if names == nil {
		names = []string{}
	}
	b, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// orderFiles sorts files into play order: by (disc, track) tags when every
// file in the book has a track number tag, otherwise by natural sort of
// rel_path.
func orderFiles(files []*fileMeta) {
	allHaveTrack := len(files) > 0
	for _, f := range files {
		if !f.hasTrack {
			allHaveTrack = false
			break
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if allHaveTrack {
			if files[i].disc != files[j].disc {
				return files[i].disc < files[j].disc
			}
			if files[i].track != files[j].track {
				return files[i].track < files[j].track
			}
		}
		return naturalLess(files[i].relPath, files[j].relPath)
	})
}

// computeScanHash hashes the sorted list of "rel_path|size|mtime" lines for
// a book's audio files, plus any sidecar files that can affect derived
// metadata (see sidecarFilesPresent/gatherHashExtras) and
// scanFormatVersion. It deliberately only depends on filesystem stat data
// (not tags), so an unchanged book can be recognised without any tag
// reads. Bumping scanFormatVersion forces every book to be re-read after a
// scanner logic change.
func computeScanHash(files []*fileMeta, extra []sidecarFile) string {
	lines := make([]string, 0, len(files)+len(extra)+1)
	lines = append(lines, fmt.Sprintf("version|%d", scanFormatVersion))
	for _, f := range files {
		lines = append(lines, fmt.Sprintf("%s|%d|%d", f.relPath, f.size, f.mtimeMs))
	}
	for _, e := range extra {
		lines = append(lines, fmt.Sprintf("sidecar:%s|%d|%d", e.name, e.size, e.mtimeMs))
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

// gatherHashExtras stats the sidecar files and the on-disk cover image
// candidate (if any) for a book folder, cheaply (no tag reads), so
// computeScanHash can be sensitive to their changes. An embedded-art
// change is already covered by its audio file's own size/mtime, which is
// always part of the hash; only a separate cover image file needs this.
func gatherHashExtras(bookDirAbs string, isSingleFile bool) []sidecarFile {
	extra := sidecarFilesPresent(bookDirAbs, isSingleFile)
	if name := candidateCoverFolderFile(bookDirAbs, isSingleFile); name != "" {
		if fi, err := os.Stat(filepath.Join(bookDirAbs, name)); err == nil {
			extra = append(extra, sidecarFile{name: "cover:" + name, size: fi.Size(), mtimeMs: fi.ModTime().UnixMilli()})
		}
	}
	return extra
}

// buildBookMeta derives book-level metadata from the first file in play
// order. folderName is the book's display name fallback (the folder's base
// name, or the filename-without-extension for a single-file book).
func buildBookMeta(first *fileMeta, folderName string) bookMeta {
	t := first.tags

	title := firstTag(t, taglib.Album)
	if title == "" {
		title = firstTag(t, taglib.Title)
	}
	if title == "" {
		title = folderName
	}

	authors := firstTag(t, taglib.AlbumArtist)
	if authors == "" {
		authors = firstTag(t, taglib.Artist)
	}

	narrators := firstTag(t, taglib.Composer)

	description := firstTag(t, taglib.Comment)

	var year *int64
	if d := firstTag(t, taglib.Date); d != "" {
		if m := yearRe.FindString(d); m != "" {
			if y, err := strconv.ParseInt(m, 10, 64); err == nil {
				year = &y
			}
		}
	}

	series := firstTag(t, "SERIES")
	seriesSeq := firstTag(t, "SERIES-PART")
	if series == "" {
		series = firstTag(t, taglib.MovementName)
		seriesSeq = firstTag(t, "MOVEMENT")
	}

	return bookMeta{
		title:         title,
		subtitle:      firstTag(t, taglib.Subtitle),
		authorsJSON:   namesJSON(splitNames(authors)),
		narratorsJSON: namesJSON(splitNames(narrators)),
		series:        series,
		seriesSeq:     seriesSeq,
		description:   description,
		publishedYear: year,
		language:      firstTag(t, taglib.Language),
		asin:          firstTag(t, taglib.ASIN),
		isbn:          firstTag(t, "ISBN"),
	}
}

// singleFileTitle returns the display title (folder_path base, minus
// extension) used as the fallback book name for a root-level single-file
// book.
func singleFileTitle(folderPath string) string {
	base := path.Base(folderPath)
	return strings.TrimSuffix(base, path.Ext(base))
}

func codecFor(absPath string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(absPath), "."))
}

// leadingSeqRe matches a leading sequence number at the start of a folder
// or title, e.g. "01 - Spin", "Book 2 - Axis", "1.5 - Interlude",
// "Vol. 3 - X". Group 1 (optional) is a "book"/"vol[ume]" prefix; group 2
// is the number; group 3 is the remainder.
var leadingSeqRe = regexp.MustCompile(`(?i)^\s*(book\s*|vol(?:ume)?\.?\s*)?(\d+(?:\.\d+)?)\s*[-.–]\s*(.+)$`)

// parseLeadingSeq extracts a leading sequence number and the remaining
// title from a folder/title name. It reports ok=false when there's no
// match, or when the number is a bare 4-digit value with no "book"/"vol"
// prefix - that looks like a publication year (e.g. "2001 - A Space
// Odyssey"), which is deliberately not treated as a sequence number.
func parseLeadingSeq(name string) (seq, title string, ok bool) {
	m := leadingSeqRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	prefix, numStr, rest := m[1], m[2], strings.TrimSpace(m[3])
	if rest == "" {
		return "", "", false
	}
	if prefix == "" && len(numStr) == 4 && !strings.Contains(numStr, ".") {
		return "", "", false
	}
	return numStr, rest, true
}

// normalizeSeq formats a sequence number string canonically ("01" -> "1",
// "1.50" -> "1.5"), falling back to the original string if it doesn't
// parse as a number.
func normalizeSeq(numStr string) string {
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return numStr
	}
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// leadingNumSpaceRe matches a leading track/sequence-like number separated
// from the rest of a title by whitespace and an optional dash, e.g.
// "01 A short history of nearly everything".
var leadingNumSpaceRe = regexp.MustCompile(`^(\d{1,3})\s+[-–]?\s*(.*)$`)

// applyTitleCleanup strips a leading "NN" / "NN - " prefix from a title
// that is still exactly the book's folder name, even outside a series
// context. It only strips when at least 3 characters remain and the
// leading number isn't 4 digits (handled structurally: the pattern only
// matches 1-3 digits).
func applyTitleCleanup(meta *bookMeta, folderName string) {
	if meta.title != folderName {
		return
	}
	m := leadingNumSpaceRe.FindStringSubmatch(meta.title)
	if m == nil {
		return
	}
	rest := strings.TrimSpace(m[2])
	if len(rest) < 3 {
		return
	}
	meta.title = rest
}

// trailingSeriesDashRe matches a "<title> - <series> Book <n>" suffix.
var trailingSeriesDashRe = regexp.MustCompile(`(?i)^(.*?)\s+-\s+(.+?)\s+Book\s+(\d+(?:\.\d+)?)\s*$`)

// trailingSeriesParenRe matches a "<title> (<series> #<n>)" suffix.
var trailingSeriesParenRe = regexp.MustCompile(`(?i)^(.*?)\s+\((.+?)\s+#(\d+(?:\.\d+)?)\)\s*$`)

// stripTrailingSeries strips a trailing " - Series Book N" or
// " (Series #N)" suffix from title, returning the series name/seq found.
func stripTrailingSeries(title string) (newTitle, series, seq string, ok bool) {
	if m := trailingSeriesDashRe.FindStringSubmatch(title); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), m[3], true
	}
	if m := trailingSeriesParenRe.FindStringSubmatch(title); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), m[3], true
	}
	return title, "", "", false
}

// isEmptyNamesJSON reports whether a namesJSON-produced string represents
// an empty list.
func isEmptyNamesJSON(s string) bool {
	return s == "" || s == "[]"
}

// applyFolderConvention fills still-empty title/series/author fields from
// the book's folder layout, relative to the library root, after tags and
// sidecar metadata have already been applied. It's the last, lowest-
// priority source of metadata.
//
//   - Depth 3 (Author/Series/Book): series = middle folder when series is
//     still empty; authors = [top folder] when authors are still empty.
//   - Depth 2 (Author/Book): authors = [top folder] when authors are still
//     empty.
//   - The book folder name (and, independently, a title that came from
//     tags/sidecars, if it matches the same pattern) is parsed for a
//     leading sequence number. When matched: series_seq is set (if empty)
//     when the folder is inside a series (depth 3) or the series is
//     otherwise known, and the title has the prefix stripped in all of
//     those cases plus the depth-2-with-no-series case (title stripped,
//     but no series_seq set).
//   - A trailing " - Series Book N" / " (Series #N)" is stripped from the
//     title if present, filling series/series_seq when empty.
//   - Finally, if the title is still exactly the folder name, a bare
//     leading "NN " prefix (no dash required) is stripped too.
//
// Single-file books (folder_path is a filename, not a directory) have no
// series/author folder structure, so only the final title cleanup applies.
func applyFolderConvention(meta *bookMeta, folderPath string, isSingleFile bool) {
	if isSingleFile {
		applyTitleCleanup(meta, singleFileTitle(folderPath))
		return
	}

	parts := strings.Split(folderPath, "/")
	depth := len(parts)
	bookFolderName := parts[depth-1]

	if depth == 3 {
		if meta.series == "" {
			meta.series = parts[1]
		}
		if isEmptyNamesJSON(meta.authorsJSON) {
			meta.authorsJSON = namesJSON([]string{parts[0]})
		}
	} else if depth == 2 {
		if isEmptyNamesJSON(meta.authorsJSON) {
			meta.authorsJSON = namesJSON([]string{parts[0]})
		}
	}

	seriesKnown := meta.series != ""
	allowStrip := depth == 3 || depth == 2 || seriesKnown
	if allowStrip {
		allowSeq := depth == 3 || seriesKnown

		if folderSeq, _, ok := parseLeadingSeq(bookFolderName); ok && allowSeq && meta.seriesSeq == "" {
			meta.seriesSeq = normalizeSeq(folderSeq)
		}
		if titleSeq, titleRest, ok := parseLeadingSeq(meta.title); ok {
			meta.title = titleRest
			if allowSeq && meta.seriesSeq == "" {
				meta.seriesSeq = normalizeSeq(titleSeq)
			}
		}
	}

	if newTitle, series, seq, ok := stripTrailingSeries(meta.title); ok {
		meta.title = newTitle
		if meta.series == "" {
			meta.series = series
		}
		if meta.seriesSeq == "" {
			meta.seriesSeq = normalizeSeq(seq)
		}
	}

	applyTitleCleanup(meta, bookFolderName)
}
