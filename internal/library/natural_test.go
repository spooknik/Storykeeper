package library

import "testing"

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.mp3", "10.mp3", true},
		{"10.mp3", "2.mp3", false},
		{"a.mp3", "a.mp3", false},
		{"01.mp3", "1.mp3", false}, // equal numeric value -> not less
		{"1.mp3", "01.mp3", false},
		{"track1.mp3", "track2.mp3", true},
		{"track2.mp3", "track10.mp3", true},
		{"CD1/01.mp3", "CD1/02.mp3", true},
		{"CD1/10.mp3", "CD2/01.mp3", true},
		{"CD2/01.mp3", "CD10/01.mp3", true},
		{"a", "ab", true},
		{"ab", "a", false},
		{"", "a", true},
		{"a", "", false},
		{"file2", "file10", true},
		{"file10", "file2", false},
		{"1", "2", true},
		{"9", "10", true},
		{"100", "20", false},
	}
	for _, c := range cases {
		got := naturalLess(c.a, c.b)
		if got != c.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
