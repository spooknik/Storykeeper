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

// seriesFacetSQL counts books per distinct series (a plain column).
func seriesFacetSQL(visible string) string {
	return `SELECT b.series AS name, COUNT(*) AS book_count
		FROM books b
		WHERE ` + visible + ` AND b.series <> ''
		GROUP BY b.series COLLATE NOCASE
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
func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	s.writeFacets(w, r, "series", seriesFacetSQL)
}
