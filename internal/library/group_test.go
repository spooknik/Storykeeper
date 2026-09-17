package library

import (
	"reflect"
	"sort"
	"testing"
)

func normalize(groups []bookGroup) []bookGroup {
	out := make([]bookGroup, len(groups))
	copy(out, groups)
	sort.Slice(out, func(i, j int) bool { return out[i].FolderPath < out[j].FolderPath })
	for i := range out {
		files := append([]string(nil), out[i].Files...)
		sort.Strings(files)
		out[i].Files = files
	}
	return out
}

func TestGroupBooks(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  []bookGroup
	}{
		{
			name:  "simple folder book",
			paths: []string{"BookA/01.mp3", "BookA/02.mp3"},
			want: []bookGroup{
				{FolderPath: "BookA", Files: []string{"01.mp3", "02.mp3"}},
			},
		},
		{
			name:  "root single file",
			paths: []string{"Solo.m4b"},
			want: []bookGroup{
				{FolderPath: "Solo.m4b", Files: []string{""}},
			},
		},
		{
			name:  "mixed root files and folder book",
			paths: []string{"Solo.m4b", "BookA/01.mp3", "BookA/02.mp3"},
			want: []bookGroup{
				{FolderPath: "BookA", Files: []string{"01.mp3", "02.mp3"}},
				{FolderPath: "Solo.m4b", Files: []string{""}},
			},
		},
		{
			name: "CD rollup",
			paths: []string{
				"BookB/CD1/01.mp3", "BookB/CD1/02.mp3",
				"BookB/CD2/01.mp3", "BookB/CD2/02.mp3",
			},
			want: []bookGroup{
				{FolderPath: "BookB", Files: []string{"CD1/01.mp3", "CD1/02.mp3", "CD2/01.mp3", "CD2/02.mp3"}},
			},
		},
		{
			name: "disc/disk/part variants roll up",
			paths: []string{
				"BookC/Disc 1/a.mp3",
				"BookC/disk-2/b.mp3",
				"BookC/Part_3/c.mp3",
			},
			want: []bookGroup{
				{FolderPath: "BookC", Files: []string{"Disc 1/a.mp3", "disk-2/b.mp3", "Part_3/c.mp3"}},
			},
		},
		{
			name: "parent with direct audio absorbs subdir audio too (shallowest-dir rule)",
			paths: []string{
				"BookD/00-intro.mp3",
				"BookD/CD1/01.mp3",
			},
			want: []bookGroup{
				{FolderPath: "BookD", Files: []string{"00-intro.mp3", "CD1/01.mp3"}},
			},
		},
		{
			name: "no rollup when subdir doesn't match cd pattern and parent has no direct audio",
			paths: []string{
				"BookE/Bonus/extra.mp3",
			},
			want: []bookGroup{
				{FolderPath: "BookE/Bonus", Files: []string{"extra.mp3"}},
			},
		},
		{
			name: "shallowest dir with direct audio absorbs a nested bonus subfolder",
			paths: []string{
				"Author/Title/a.mp3",
				"Author/Title/Bonus/b.mp3",
			},
			want: []bookGroup{
				{FolderPath: "Author/Title", Files: []string{"a.mp3", "Bonus/b.mp3"}},
			},
		},
		{
			name: "CD rollup two levels deep under Author/Title",
			paths: []string{
				"Author/Title/CD1/a.mp3",
				"Author/Title/CD2/b.mp3",
			},
			want: []bookGroup{
				{FolderPath: "Author/Title", Files: []string{"CD1/a.mp3", "CD2/b.mp3"}},
			},
		},
		{
			name: "Disc-pattern subdir merges with a sibling file when Title has direct audio",
			paths: []string{
				"Author/Title/Disc 1/a.mp3",
				"Author/Title/extra.mp3",
			},
			want: []bookGroup{
				{FolderPath: "Author/Title", Files: []string{"Disc 1/a.mp3", "extra.mp3"}},
			},
		},
		{
			name: "cd-pattern dirs directly under library root are not rolled into root",
			paths: []string{
				"CD1/01.mp3",
				"CD2/01.mp3",
			},
			want: []bookGroup{
				{FolderPath: "CD1", Files: []string{"01.mp3"}},
				{FolderPath: "CD2", Files: []string{"01.mp3"}},
			},
		},
		{
			name:  "empty input",
			paths: nil,
			want:  nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalize(groupBooks(c.paths))
			want := normalize(c.want)
			if len(got) == 0 {
				got = nil
			}
			if len(want) == 0 {
				want = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("groupBooks(%v) =\n  %#v\nwant\n  %#v", c.paths, got, want)
			}
		})
	}
}
