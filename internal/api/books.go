package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/media"
)

func coverURL(id int64, coverPath string) string {
	if coverPath == "" {
		return ""
	}
	return fmt.Sprintf("/media/books/%d/cover?size=600", id)
}

func fileURL(bookID int64, idx int) string {
	return fmt.Sprintf("/media/books/%d/files/%d", bookID, idx)
}

func jsonStrings(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

const bookSelect = `
	SELECT b.id, b.library_id, b.title, b.subtitle, b.authors, b.narrators, b.series, b.series_seq,
	       b.duration_ms, b.cover_path, b.added_at, b.updated_at,
	       p.position_ms, p.duration_ms, p.file_index, p.seq, p.listened_at, p.device_id, p.device_name, p.finished
	FROM books b
	LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?`

type bookRow struct {
	BookSummary
	coverPath string
}

func scanBookRow(sc interface{ Scan(...any) error }) (*bookRow, error) {
	var b bookRow
	var authors, narrators string
	var pPos, pDur, pSeq, pListened sql.NullInt64
	var pFile sql.NullInt64
	var pDev, pDevName sql.NullString
	var pFin sql.NullBool
	err := sc.Scan(&b.ID, &b.LibraryID, &b.Title, &b.Subtitle, &authors, &narrators, &b.Series, &b.SeriesSeq,
		&b.DurationMs, &b.coverPath, &b.AddedAt, &b.UpdatedAt,
		&pPos, &pDur, &pFile, &pSeq, &pListened, &pDev, &pDevName, &pFin)
	if err != nil {
		return nil, err
	}
	b.Authors = jsonStrings(authors)
	b.Narrators = jsonStrings(narrators)
	b.CoverURL = coverURL(b.ID, b.coverPath)
	if pPos.Valid {
		b.Progress = &Progress{
			BookID: b.ID, PositionMs: pPos.Int64, DurationMs: pDur.Int64, FileIndex: int(pFile.Int64),
			Seq: pSeq.Int64, ListenedAt: pListened.Int64, DeviceID: pDev.String, DeviceName: pDevName.String,
			Finished: pFin.Bool,
		}
	}
	return &b, nil
}

// inProgressCond matches books the user has started but not finished. A row
// at position 0 is not a listen: setting a per-book rate creates one before
// the first play, and that must not put the book on the continue shelf.
const inProgressCond = "p.position_ms > 0 AND p.finished = 0"

// notStartedCond is the complement: no row, or a row nothing has been heard on.
const notStartedCond = "(p.position_ms IS NULL OR (p.position_ms = 0 AND p.finished = 0))"

// jsonArrayHas matches an exact, case-insensitive member of a JSON array column.
func jsonArrayHas(col string) string {
	return "EXISTS (SELECT 1 FROM json_each(" + col + ") WHERE value = ? COLLATE NOCASE)"
}

// orderClause maps sort/dir to an ORDER BY body. Unknown sort falls back to
// title, unknown dir to asc. dir=desc flips the primary key only: the grouping
// prefix (series-less / never-listened rows last) and the tie-breakers always
// stay ascending so results remain stable.
func orderClause(sort, dir string) string {
	var prefix, primary, tie string
	switch sort {
	case "author":
		primary = "json_extract(b.authors,'$[0]') COLLATE NOCASE"
		tie = "b.title COLLATE NOCASE"
	case "series":
		prefix = "b.series = ''" // books without a series sort last
		primary = "b.series COLLATE NOCASE"
		tie = "CAST(b.series_seq AS REAL), b.title COLLATE NOCASE"
	case "added":
		primary = "b.added_at"
	case "recent":
		prefix = "p.listened_at IS NULL" // NULLs last regardless of dir
		primary = "p.listened_at"
	case "duration":
		primary = "b.duration_ms"
	default:
		primary = "b.title COLLATE NOCASE"
	}
	// "Recently added" and "recently listened" only make sense newest-first,
	// so those default to desc when no direction is given.
	if dir == "" && (sort == "added" || sort == "recent") {
		dir = "desc"
	}
	if dir == "desc" {
		primary += " DESC"
	} else {
		primary += " ASC"
	}
	parts := make([]string, 0, 3)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, primary)
	if tie != "" {
		parts = append(parts, tie)
	}
	return strings.Join(parts, ", ")
}

// GET /api/v1/books?library=&q=&sort=title|author|series|added|recent|duration&dir=asc|desc
//
//	&author=&series=&narrator=&in_progress=1&finished=1|0&not_started=1&limit=&offset=
func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	q := r.URL.Query()

	where, args := visibleClause(u, "b.library_id")
	conds := []string{where}
	if lib := queryInt(r, "library", 0); lib > 0 {
		conds = append(conds, "b.library_id = ?")
		args = append(args, lib)
	}
	if term := strings.TrimSpace(q.Get("q")); term != "" {
		like := "%" + strings.ReplaceAll(strings.ReplaceAll(term, "%", "\\%"), "_", "\\_") + "%"
		conds = append(conds, `(b.title LIKE ? ESCAPE '\' OR b.authors LIKE ? ESCAPE '\' OR b.series LIKE ? ESCAPE '\' OR b.narrators LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}
	if v := strings.TrimSpace(q.Get("author")); v != "" {
		conds = append(conds, jsonArrayHas("b.authors"))
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Get("narrator")); v != "" {
		conds = append(conds, jsonArrayHas("b.narrators"))
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.Get("series")); v != "" {
		conds = append(conds, "b.series = ? COLLATE NOCASE")
		args = append(args, v)
	}
	if q.Get("in_progress") == "1" {
		conds = append(conds, inProgressCond)
	}
	switch q.Get("finished") {
	case "1":
		conds = append(conds, "p.finished = 1")
	case "0":
		conds = append(conds, inProgressCond)
	}
	if q.Get("not_started") == "1" {
		conds = append(conds, notStartedCond)
	}
	order := orderClause(q.Get("sort"), q.Get("dir"))
	limit := queryInt(r, "limit", 100)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := max(queryInt(r, "offset", 0), 0)

	whereSQL := " WHERE " + strings.Join(conds, " AND ")
	// Count uses the same joins and conditions so p.* filters stay in sync.
	countArgs := append([]any{u.ID}, args...)
	var total int
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM books b LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?`+whereSQL,
		countArgs...).Scan(&total); err != nil {
		s.Log.Error("count books", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	listArgs := append(append([]any{u.ID}, args...), limit, offset)
	rows, err := s.DB.QueryContext(r.Context(), bookSelect+whereSQL+" ORDER BY "+order+" LIMIT ? OFFSET ?", listArgs...)
	if err != nil {
		s.Log.Error("list books", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := BookList{Items: []BookSummary{}, Total: total}
	for rows.Next() {
		b, err := scanBookRow(rows)
		if err != nil {
			s.Log.Error("scan book", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		out.Items = append(out.Items, b.BookSummary)
	}
	writeJSON(w, http.StatusOK, out)
}

// loadBook returns the visible book or nil.
func (s *Server) loadBook(r *http.Request, id int64) (*bookRow, error) {
	u, _ := auth.FromContext(r.Context())
	where, args := visibleClause(u, "b.library_id")
	args = append([]any{u.ID, id}, args...)
	row := s.DB.QueryRowContext(r.Context(), bookSelect+" WHERE b.id = ? AND "+where, args...)
	b, err := scanBookRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	b, err := s.loadBook(r, id)
	if err != nil {
		s.Log.Error("get book", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return
	}
	d := BookDetail{BookSummary: b.BookSummary, Files: []BookFile{}, Chapters: []Chapter{}, Bookmarks: []Bookmark{}}
	var year sql.NullInt64
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT description, published_year, language, asin, isbn FROM books WHERE id = ?`, id).
		Scan(&d.Description, &year, &d.Language, &d.ASIN, &d.ISBN); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if year.Valid {
		y := int(year.Int64)
		d.PublishedYear = &y
	}

	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT idx, rel_path, size, duration_ms, codec, bitrate FROM book_files WHERE book_id = ? ORDER BY idx`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	for rows.Next() {
		var f BookFile
		if err := rows.Scan(&f.Index, &f.RelPath, &f.Size, &f.DurationMs, &f.Codec, &f.Bitrate); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		f.URL = fileURL(id, f.Index)
		d.Files = append(d.Files, f)
	}
	rows.Close()

	rows, err = s.DB.QueryContext(r.Context(),
		`SELECT idx, title, start_ms, end_ms FROM chapters WHERE book_id = ? ORDER BY idx`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.Index, &c.Title, &c.StartMs, &c.EndMs); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		d.Chapters = append(d.Chapters, c)
	}
	rows.Close()

	u, _ := auth.FromContext(r.Context())
	rows, err = s.DB.QueryContext(r.Context(),
		`SELECT id, book_id, position_ms, note, created_at FROM bookmarks WHERE user_id = ? AND book_id = ? ORDER BY position_ms`, u.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	for rows.Next() {
		var bm Bookmark
		if err := rows.Scan(&bm.ID, &bm.BookID, &bm.PositionMs, &bm.Note, &bm.CreatedAt); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		d.Bookmarks = append(d.Bookmarks, bm)
	}
	rows.Close()

	writeJSON(w, http.StatusOK, d)
}

// GET /media/books/{id}/files/{idx}
func (s *Server) serveBookFile(w http.ResponseWriter, r *http.Request) {
	id, ok1 := pathInt(r, "id")
	idx, ok2 := pathInt(r, "idx")
	if !ok1 || !ok2 {
		http.NotFound(w, r)
		return
	}
	b, err := s.loadBook(r, id)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	var libPath, folder, rel string
	err = s.DB.QueryRowContext(r.Context(), `
		SELECT l.path, b.folder_path, f.rel_path
		FROM book_files f JOIN books b ON b.id = f.book_id JOIN libraries l ON l.id = b.library_id
		WHERE f.book_id = ? AND f.idx = ?`, id, idx).Scan(&libPath, &folder, &rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	abs := media.SafeJoin(libPath, folder, rel)
	if abs == "" {
		http.NotFound(w, r)
		return
	}
	media.ServeAudio(w, r, abs)
}

// GET /media/books/{id}/cover?size=600|200
func (s *Server) serveBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := s.loadBook(r, id)
	if err != nil || b == nil || b.coverPath == "" {
		http.NotFound(w, r)
		return
	}
	abs := media.SafeJoin(s.Cfg.DataDir, b.coverPath)
	if abs == "" {
		http.NotFound(w, r)
		return
	}
	size := queryInt(r, "size", 600)
	if size != 200 && size != 600 && size != 0 {
		size = 600
	}
	media.ServeCover(w, r, abs, size)
}
