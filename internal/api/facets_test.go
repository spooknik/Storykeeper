package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
)

func facetsAs(t *testing.T, s *Server, u *auth.User, h http.HandlerFunc, path string) ([]Facet, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, reqAs(http.MethodGet, path, nil, u, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body=%s", path, w.Code, w.Body.String())
	}
	var out []Facet
	decodeBody(t, w, &out)
	return out, strings.TrimSpace(w.Body.String())
}

func TestFacets_CountsAndOrder(t *testing.T) {
	f := newBookFixture(t)

	// A book with two authors counts once under each of them.
	authors, _ := facetsAs(t, f.s, f.reader, f.s.listAuthors, "/api/v1/authors")
	wantAuthors := []Facet{
		{Name: "Adam Author", BookCount: 2}, // Beta Two, Delta Solo
		{Name: "Mia Author", BookCount: 2},  // Gamma Ten, Epsilon Other
		{Name: "Zoe Author", BookCount: 2},  // Alpha One, Beta Two
	}
	if !reflect.DeepEqual(authors, wantAuthors) {
		t.Fatalf("authors = %+v, want %+v", authors, wantAuthors)
	}

	series, _ := facetsAs(t, f.s, f.reader, f.s.listSeries, "/api/v1/series")
	wantSeries := []Facet{
		{Name: "Chronicles", BookCount: 3},
		{Name: "Other Series", BookCount: 1},
	}
	if !reflect.DeepEqual(series, wantSeries) {
		t.Fatalf("series = %+v, want %+v", series, wantSeries)
	}

	narrators, _ := facetsAs(t, f.s, f.reader, f.s.listNarrators, "/api/v1/narrators")
	wantNarrators := []Facet{
		{Name: "Nate Narrator", BookCount: 3},    // Alpha One, Beta Two, Delta Solo
		{Name: "Ophelia Narrator", BookCount: 2}, // Gamma Ten, Epsilon Other
	}
	if !reflect.DeepEqual(narrators, wantNarrators) {
		t.Fatalf("narrators = %+v, want %+v", narrators, wantNarrators)
	}
}

func TestFacets_RestrictedLibraryHiddenFromNonAdmin(t *testing.T) {
	f := newBookFixture(t)

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
		secret  string
	}{
		{"authors", f.s.listAuthors, "/api/v1/authors", "Secret Author"},
		{"series", f.s.listSeries, "/api/v1/series", "Secret Series"},
		{"narrators", f.s.listNarrators, "/api/v1/narrators", "Secret Narrator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := facetsAs(t, f.s, f.reader, tc.handler, tc.path)
			for _, fa := range reader {
				if fa.Name == tc.secret {
					t.Fatalf("non-admin saw %q in %+v", tc.secret, reader)
				}
			}
			admin, _ := facetsAs(t, f.s, f.admin, tc.handler, tc.path)
			found := false
			for _, fa := range admin {
				if fa.Name == tc.secret {
					found = true
					if fa.BookCount != 1 {
						t.Fatalf("%q count = %d, want 1", tc.secret, fa.BookCount)
					}
				}
			}
			if !found {
				t.Fatalf("admin did not see %q in %+v", tc.secret, admin)
			}
		})
	}
}

func TestFacets_EmptyIsArrayNotNull(t *testing.T) {
	s := newTestServer(t)
	u := mustCreateUser(t, s, "reader", "password123", auth.RoleUser)
	// A book with empty metadata contributes nothing to any facet.
	lib := mustCreateLibrary(t, s, "open")
	mustCreateBook(t, s, lib, "Bare Book", 1000)

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{"authors", s.listAuthors, "/api/v1/authors"},
		{"series", s.listSeries, "/api/v1/series"},
		{"narrators", s.listNarrators, "/api/v1/narrators"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, raw := facetsAs(t, s, u, tc.handler, tc.path)
			if len(got) != 0 {
				t.Fatalf("facets = %+v, want empty", got)
			}
			if raw != "[]" {
				t.Fatalf("body = %s, want []", raw)
			}
		})
	}
}
