package api

import (
	"net/http"

	"github.com/spooknik/storykeeper/internal/auth"
)

// jsonFacetSQL counts books per distinct member of a JSON array column
// (authors, narrators). col is a package constant, never user input.
func jsonFacetSQL(col, visible string) string {
	return `SELECT value AS name, COUNT(*) AS book_count
		FROM books b, json_each(b.` + col + `)
		WHERE ` + visible + ` AND value <> ''
		GROUP BY value COLLATE NOCASE
		ORDER BY name COLLATE NOCASE`
}

// writeFacets runs a facet query and writes []Facet (never null).
func (s *Server) writeFacets(w http.ResponseWriter, r *http.Request, what string, build func(visible string) string) {
	u, _ := auth.FromContext(r.Context())
	visible, args := visibleClause(u, "b.library_id")
	rows, err := s.DB.QueryContext(r.Context(), build(visible), args...)
	if err != nil {
		s.Log.Error("list facets", "facet", what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []Facet{}
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Name, &f.BookCount); err != nil {
			s.Log.Error("scan facet", "facet", what, "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		s.Log.Error("list facets", "facet", what, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/authors
func (s *Server) listAuthors(w http.ResponseWriter, r *http.Request) {
	s.writeFacets(w, r, "authors", func(visible string) string { return jsonFacetSQL("authors", visible) })
}

// GET /api/v1/narrators
func (s *Server) listNarrators(w http.ResponseWriter, r *http.Request) {
	s.writeFacets(w, r, "narrators", func(visible string) string { return jsonFacetSQL("narrators", visible) })
}

// GET /api/v1/series
//
// Unlike authors/narrators, series carry per-user progress counts, so this
// handler does not go through the generic writeFacets/Facet path.
func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	visible, visArgs := visibleClause(u, "b.library_id")
	query := `SELECT b.series AS name,
			COUNT(*) AS book_count,
			COUNT(CASE WHEN p.finished = 1 THEN 1 END) AS finished_count,
			COUNT(CASE WHEN p.book_id IS NOT NULL AND p.finished = 0 AND p.position_ms > 0 THEN 1 END) AS in_progress_count
		FROM books b
		LEFT JOIN progress p ON p.book_id = b.id AND p.user_id = ?
		WHERE ` + visible + ` AND b.series <> ''
		GROUP BY b.series COLLATE NOCASE
		ORDER BY name COLLATE NOCASE`
	args := append([]any{u.ID}, visArgs...)
	rows, err := s.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		s.Log.Error("list facets", "facet", "series", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []SeriesFacet{}
	for rows.Next() {
		var f SeriesFacet
		if err := rows.Scan(&f.Name, &f.BookCount, &f.FinishedCount, &f.InProgressCount); err != nil {
			s.Log.Error("scan facet", "facet", "series", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		s.Log.Error("list facets", "facet", "series", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
