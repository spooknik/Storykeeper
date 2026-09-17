package library

import "testing"

func TestParseLeadingSeq(t *testing.T) {
	cases := []struct {
		name      string
		wantSeq   string
		wantTitle string
		wantOK    bool
	}{
		{"01 - Spin", "1", "Spin", true},
		{"Book 2 - Axis", "2", "Axis", true},
		{"1.5 - Interlude", "1.5", "Interlude", true},
		{"Vol. 3 - X", "3", "X", true},
		{"Spin", "", "", false},
		// Ambiguous: a bare 4-digit number looks like a publication year,
		// so it must NOT be treated as a sequence number.
		{"2001 - A Space Odyssey", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seq, title, ok := parseLeadingSeq(c.name)
			if ok != c.wantOK {
				t.Fatalf("parseLeadingSeq(%q) ok = %v, want %v", c.name, ok, c.wantOK)
			}
			if !ok {
				return
			}
			if title != c.wantTitle {
				t.Errorf("parseLeadingSeq(%q) title = %q, want %q", c.name, title, c.wantTitle)
			}
			if got := normalizeSeq(seq); got != c.wantSeq {
				t.Errorf("parseLeadingSeq(%q) seq (normalized) = %q, want %q", c.name, got, c.wantSeq)
			}
		})
	}
}

func TestNormalizeSeq(t *testing.T) {
	cases := []struct{ in, want string }{
		{"01", "1"},
		{"2", "2"},
		{"1.5", "1.5"},
		{"1.50", "1.5"},
		{"3", "3"},
		{"not-a-number", "not-a-number"},
	}
	for _, c := range cases {
		if got := normalizeSeq(c.in); got != c.want {
			t.Errorf("normalizeSeq(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
