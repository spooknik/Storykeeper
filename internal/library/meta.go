package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

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
// a book's files. It deliberately only depends on filesystem stat data (not
// tags), so an unchanged book can be recognised without any tag reads.
func computeScanHash(files []*fileMeta) string {
	lines := make([]string, len(files))
	for i, f := range files {
		lines[i] = fmt.Sprintf("%s|%d|%d", f.relPath, f.size, f.mtimeMs)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
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
