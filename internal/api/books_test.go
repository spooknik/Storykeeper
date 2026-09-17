package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
)

// bookSeed describes the extra metadata seedBook writes on top of the minimal
// row mustCreateBook inserts.
type bookSeed struct {
	title      string
	authors    []string
	narrators  []string
	series     string
	seriesSeq  string
	durationMs int64
	addedAt    int64
}

// seedBook inserts a book with full metadata, reusing mustCreateBook.
func seedBook(t *testing.T, s *Server, libID int64, sp bookSeed) int64 {
	t.Helper()
	id := mustCreateBook(t, s, libID, sp.title, sp.durationMs)
	if sp.authors == nil {
		sp.authors = []string{}
	}
	if sp.narrators == nil {
		sp.narrators = []string{}
	}
	a, _ := json.Marshal(sp.authors)
	n, _ := json.Marshal(sp.narrators)
	ctx := context.Background()
	if err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE books SET authors = ?, narrators = ?, series = ?, series_seq = ?, added_at = ? WHERE id = ?`,
			string(a), string(n), sp.series, sp.seriesSeq, sp.addedAt, id)
		return err
	}); err != nil {
		t.Fatalf("seed book %q: %v", sp.title, err)
	}
	return id
}

// seedProgress writes a progress row for (user, book).
func seedProgress(t *testing.T, s *Server, userID, bookID, positionMs, listenedAt int64, finished bool) {
	t.Helper()
	ctx := context.Background()
	fin := 0
	if finished {
		fin = 1
	}
	if err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO progress
			(user_id, book_id, position_ms, duration_ms, file_index, seq, listened_at, received_at, device_id, device_name, finished)
			VALUES (?, ?, ?, 0, 0, 1, ?, ?, 'dev', '', ?)`,
			userID, bookID, positionMs, listenedAt, listenedAt, fin)
		return err
	}); err != nil {
		t.Fatalf("seed progress: %v", err)
	}
}

const seedBaseTime = int64(1_700_000_000_000)

// bookFixture is the shared corpus: five visible books in an open library plus
// one book in a library restricted to the admin.
type bookFixture struct {
	s        *Server
	admin    *auth.User
	reader   *auth.User
	openLib  int64
	secretLb int64
}

// newBookFixture seeds books, authors, series, narrators and progress rows.
//
//	Alpha One     Zoe Author              Chronicles #1   1000ms  in progress
//	Beta Two      Adam Author, Zoe Author Chronicles #2   5000ms  finished
//	Gamma Ten     Mia Author              Chronicles #10  3000ms  not started
//	Delta Solo    Adam Author             (no series)     2000ms  not started
//	Epsilon Other Mia Author              Other Series #1 4000ms  not started
//	Zeta Secret   Secret Author           Secret Series   6000ms  (restricted lib)
func newBookFixture(t *testing.T) *bookFixture {
	t.Helper()
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	reader := mustCreateUser(t, s, "reader", "password123", auth.RoleUser)
	openLib := mustCreateLibrary(t, s, "open")
	secretLib := mustCreateLibrary(t, s, "secret", admin.ID)

	alpha := seedBook(t, s, openLib, bookSeed{
		title: "Alpha One", authors: []string{"Zoe Author"}, narrators: []string{"Nate Narrator"},
		series: "Chronicles", seriesSeq: "1", durationMs: 1000, addedAt: seedBaseTime + 100,
	})
	beta := seedBook(t, s, openLib, bookSeed{
		title: "Beta Two", authors: []string{"Adam Author", "Zoe Author"}, narrators: []string{"Nate Narrator"},
		series: "Chronicles", seriesSeq: "2", durationMs: 5000, addedAt: seedBaseTime + 200,
	})
	seedBook(t, s, openLib, bookSeed{
		title: "Gamma Ten", authors: []string{"Mia Author"}, narrators: []string{"Ophelia Narrator"},
		series: "Chronicles", seriesSeq: "10", durationMs: 3000, addedAt: seedBaseTime + 300,
	})
	seedBook(t, s, openLib, bookSeed{
		title: "Delta Solo", authors: []string{"Adam Author"}, narrators: []string{"Nate Narrator"},
		durationMs: 2000, addedAt: seedBaseTime + 400,
	})
	seedBook(t, s, openLib, bookSeed{
		title: "Epsilon Other", authors: []string{"Mia Author"}, narrators: []string{"Ophelia Narrator"},
		series: "Other Series", seriesSeq: "1", durationMs: 4000, addedAt: seedBaseTime + 500,
	})
	seedBook(t, s, secretLib, bookSeed{
		title: "Zeta Secret", authors: []string{"Secret Author"}, narrators: []string{"Secret Narrator"},
		series: "Secret Series", seriesSeq: "1", durationMs: 6000, addedAt: seedBaseTime + 600,
	})

	seedProgress(t, s, reader.ID, alpha, 500, seedBaseTime+10, false) // in progress
	seedProgress(t, s, reader.ID, beta, 5000, seedBaseTime+20, true)  // finished

	return &bookFixture{s: s, admin: admin, reader: reader, openLib: openLib, secretLb: secretLib}
}

// listBooksAs calls listBooks with the raw query string (including "?").
func listBooksAs(t *testing.T, s *Server, u *auth.User, query string) BookList {
	t.Helper()
	w := httptest.NewRecorder()
	s.listBooks(w, reqAs(http.MethodGet, "/api/v1/books"+query, nil, u, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("listBooks%s status = %d, want 200; body=%s", query, w.Code, w.Body.String())
	}
	var out BookList
	decodeBody(t, w, &out)
	return out
}

func titlesOf(items []BookSummary) []string {
	out := make([]string, 0, len(items))
	for _, b := range items {
		out = append(out, b.Title)
	}
	return out
}

func TestListBooks_Sorting(t *testing.T) {
	f := newBookFixture(t)

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"default is title asc", "", []string{"Alpha One", "Beta Two", "Delta Solo", "Epsilon Other", "Gamma Ten"}},
		{"title desc", "?sort=title&dir=desc", []string{"Gamma Ten", "Epsilon Other", "Delta Solo", "Beta Two", "Alpha One"}},
		// First author, then title. Adam, Adam, Mia, Mia, Zoe.
		{"author asc", "?sort=author", []string{"Beta Two", "Delta Solo", "Epsilon Other", "Gamma Ten", "Alpha One"}},
		{"author desc", "?sort=author&dir=desc", []string{"Alpha One", "Epsilon Other", "Gamma Ten", "Beta Two", "Delta Solo"}},
		// Numeric seq order beats lexical ("10" after "2"); no-series book last.
		{"series asc", "?sort=series", []string{"Alpha One", "Beta Two", "Gamma Ten", "Epsilon Other", "Delta Solo"}},
		{"series desc keeps seq asc and no-series last", "?sort=series&dir=desc",
			[]string{"Epsilon Other", "Alpha One", "Beta Two", "Gamma Ten", "Delta Solo"}},
		{"added asc", "?sort=added&dir=asc", []string{"Alpha One", "Beta Two", "Gamma Ten", "Delta Solo", "Epsilon Other"}},
		{"added default desc", "?sort=added", []string{"Epsilon Other", "Delta Solo", "Gamma Ten", "Beta Two", "Alpha One"}},
		{"duration asc", "?sort=duration", []string{"Alpha One", "Delta Solo", "Gamma Ten", "Epsilon Other", "Beta Two"}},
		{"duration desc", "?sort=duration&dir=desc", []string{"Beta Two", "Epsilon Other", "Gamma Ten", "Delta Solo", "Alpha One"}},
		// Unknown sort/dir fall back to title asc.
		{"unknown sort falls back to title", "?sort=bogus&dir=sideways",
			[]string{"Alpha One", "Beta Two", "Delta Solo", "Epsilon Other", "Gamma Ten"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := titlesOf(listBooksAs(t, f.s, f.reader, tc.query).Items)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("titles = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListBooks_SortRecentNullsLastBothDirections(t *testing.T) {
	f := newBookFixture(t)

	// asc: oldest listened first, then the three never-listened books.
	asc := titlesOf(listBooksAs(t, f.s, f.reader, "?sort=recent&dir=asc").Items)
	if len(asc) != 5 || asc[0] != "Alpha One" || asc[1] != "Beta Two" {
		t.Fatalf("recent asc = %v, want [Alpha One Beta Two ...]", asc)
	}
	// desc: newest listened first, NULLs still last.
	desc := titlesOf(listBooksAs(t, f.s, f.reader, "?sort=recent&dir=desc").Items)
	if len(desc) != 5 || desc[0] != "Beta Two" || desc[1] != "Alpha One" {
		t.Fatalf("recent desc = %v, want [Beta Two Alpha One ...]", desc)
	}
	for _, order := range [][]string{asc, desc} {
		tail := map[string]bool{order[2]: true, order[3]: true, order[4]: true}
		want := map[string]bool{"Gamma Ten": true, "Delta Solo": true, "Epsilon Other": true}
		if !reflect.DeepEqual(tail, want) {
			t.Fatalf("never-listened tail = %v, want %v", tail, want)
		}
	}
}

func TestListBooks_Filters(t *testing.T) {
	f := newBookFixture(t)

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		// Exact and case-insensitive; matches a book where it is the second author.
		{"author", "?author=zoe%20author", []string{"Alpha One", "Beta Two"}},
		{"author exact only", "?author=Zoe", nil},
		{"author second position", "?author=ADAM%20AUTHOR", []string{"Beta Two", "Delta Solo"}},
		{"series", "?series=chronicles", []string{"Alpha One", "Beta Two", "Gamma Ten"}},
		{"narrator", "?narrator=OPHELIA%20NARRATOR", []string{"Epsilon Other", "Gamma Ten"}},
		{"in_progress", "?in_progress=1", []string{"Alpha One"}},
		{"finished=1", "?finished=1", []string{"Beta Two"}},
		{"finished=0", "?finished=0", []string{"Alpha One"}},
		{"not_started", "?not_started=1", []string{"Delta Solo", "Epsilon Other", "Gamma Ten"}},
		// Filters combine with AND.
		{"series and narrator", "?series=chronicles&narrator=nate%20narrator", []string{"Alpha One", "Beta Two"}},
		{"author and not_started", "?author=mia%20author&not_started=1", []string{"Epsilon Other", "Gamma Ten"}},
		{"q still works", "?q=solo", []string{"Delta Solo"}},
		{"restricted book stays hidden", "?author=secret%20author", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := listBooksAs(t, f.s, f.reader, tc.query)
			got := titlesOf(out.Items)
			want := tc.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("titles = %v, want %v", got, want)
			}
			if out.Total != len(want) {
				t.Fatalf("total = %d, want %d", out.Total, len(want))
			}
		})
	}
}

func TestListBooks_TotalMatchesFilterNotPage(t *testing.T) {
	f := newBookFixture(t)

	out := listBooksAs(t, f.s, f.reader, "?series=chronicles&sort=series&limit=1")
	if out.Total != 3 {
		t.Fatalf("total = %d, want 3", out.Total)
	}
	if got := titlesOf(out.Items); !reflect.DeepEqual(got, []string{"Alpha One"}) {
		t.Fatalf("page 1 = %v, want [Alpha One]", got)
	}
	out = listBooksAs(t, f.s, f.reader, "?series=chronicles&sort=series&limit=1&offset=2")
	if out.Total != 3 {
		t.Fatalf("total = %d, want 3", out.Total)
	}
	if got := titlesOf(out.Items); !reflect.DeepEqual(got, []string{"Gamma Ten"}) {
		t.Fatalf("page 3 = %v, want [Gamma Ten]", got)
	}

	// Unfiltered: the reader sees five of the six books.
	if all := listBooksAs(t, f.s, f.reader, ""); all.Total != 5 {
		t.Fatalf("unfiltered total = %d, want 5", all.Total)
	}
	// The admin sees the restricted library too.
	if all := listBooksAs(t, f.s, f.admin, ""); all.Total != 6 {
		t.Fatalf("admin total = %d, want 6", all.Total)
	}
}

func TestListBooks_LibraryFilter(t *testing.T) {
	f := newBookFixture(t)

	out := listBooksAs(t, f.s, f.admin, "?library="+strconv.FormatInt(f.secretLb, 10))
	if got := titlesOf(out.Items); !reflect.DeepEqual(got, []string{"Zeta Secret"}) {
		t.Fatalf("admin secret library = %v, want [Zeta Secret]", got)
	}
	// A non-admin asking for the restricted library gets nothing.
	out = listBooksAs(t, f.s, f.reader, "?library="+strconv.FormatInt(f.secretLb, 10))
	if len(out.Items) != 0 || out.Total != 0 {
		t.Fatalf("reader secret library = %v (total %d), want empty", titlesOf(out.Items), out.Total)
	}
}
