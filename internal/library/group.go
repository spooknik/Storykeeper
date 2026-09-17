package library

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// cdPattern matches directory names like "CD1", "Disc 2", "disk-3", "Part_4".
var cdPattern = regexp.MustCompile(`(?i)^(cd|disc|disk|part)[ _-]?\d+`)

// bookGroup describes one book discovered while walking a library root.
type bookGroup struct {
	// FolderPath is relative to the library root (forward-slash separated).
	// For a root-level single-file book, FolderPath is the filename itself
	// (see the package doc comment on the '' rel_path convention).
	FolderPath string
	// Files are relative to FolderPath (forward-slash separated). A single
	// "" entry means the book is one audio file living directly at
	// FolderPath (the root-level single-file case).
	Files []string
}

// groupBooks groups a flat list of audio file paths (relative to the
// library root, forward-slash separated, as produced by walkAudioFiles)
// into books.
//
// Rules (see docs on the package's scan behaviour):
//   - A directory that directly contains audio files is a book; the book
//     folder is that directory.
//   - Exception: if a directory has no audio files of its own but has
//     subdirectories matching cdPattern (CD1, Disc 2, ...) that themselves
//     contain audio, the parent directory is a single book spanning those
//     subdirs. This rollup is not applied when the parent would be the
//     library root itself ("."), since a root-level book's folder_path is
//     reserved for the single-file convention below.
//   - An audio file sitting directly in the library root becomes its own
//     single-file book, keyed by its own filename, with a lone "" entry in
//     Files (meaning: the book folder path IS the file).
//
// The returned slice and each group's Files are sorted deterministically
// (book folders by string order, files by natural order) so callers get
// stable results; final on-disk play order is decided later using tag data
// where available.
func groupBooks(relPaths []string) []bookGroup {
	byDir := map[string][]string{}
	for _, p := range relPaths {
		d := path.Dir(p)
		byDir[d] = append(byDir[d], p)
	}

	// Directories eligible to be rolled into a CD/Disc/Part-spanning parent
	// book: they match the pattern, and their parent has no direct audio
	// files of its own and is not the library root.
	rolledInto := map[string]string{}
	for d := range byDir {
		if d == "." {
			continue
		}
		base := path.Base(d)
		parent := path.Dir(d)
		if parent == "." {
			continue
		}
		if !cdPattern.MatchString(base) {
			continue
		}
		if _, parentHasFiles := byDir[parent]; parentHasFiles {
			continue
		}
		rolledInto[d] = parent
	}

	groups := map[string]*bookGroup{}
	var order []string
	addFile := func(folder, relToFolder string) {
		g, ok := groups[folder]
		if !ok {
			g = &bookGroup{FolderPath: folder}
			groups[folder] = g
			order = append(order, folder)
		}
		g.Files = append(g.Files, relToFolder)
	}

	for d, files := range byDir {
		switch {
		case d == ".":
			for _, f := range files {
				addFile(f, "")
			}
		default:
			if parent, ok := rolledInto[d]; ok {
				for _, f := range files {
					relToParent := strings.TrimPrefix(f, parent+"/")
					addFile(parent, relToParent)
				}
				continue
			}
			for _, f := range files {
				relToFolder := strings.TrimPrefix(f, d+"/")
				addFile(d, relToFolder)
			}
		}
	}

	sort.Strings(order)
	result := make([]bookGroup, 0, len(order))
	for _, k := range order {
		g := groups[k]
		if len(g.Files) > 1 || (len(g.Files) == 1 && g.Files[0] != "") {
			sort.Slice(g.Files, func(i, j int) bool { return naturalLess(g.Files[i], g.Files[j]) })
		}
		result = append(result, *g)
	}
	return result
}
