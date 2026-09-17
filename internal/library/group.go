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
//   - The book folder for a given audio file is the SHALLOWEST directory on
//     its path (starting just below the library root) that directly
//     contains at least one audio file. Every audio file anywhere beneath
//     that directory - no matter how deeply nested - belongs to the same
//     book, with its rel_path kept relative to the book folder (e.g.
//     "Bonus/extra.mp3"). This keeps things like a book's bonus-material
//     subfolder, or a "Disc 1"-style split alongside a stray extra file
//     directly in the book folder, together as one book.
//   - Exception: if a directory has no audio files of its own but has
//     subdirectories matching cdPattern (CD1, Disc 2, ...) that themselves
//     lead to audio, the parent directory is treated as if it directly
//     contained audio, becoming a single book spanning those subdirs. This
//     rollup is not applied when the parent would be the library root
//     itself ("."), since a root-level book's folder_path is reserved for
//     the single-file convention below.
//   - An audio file sitting directly in the library root becomes its own
//     single-file book, keyed by its own filename, with a lone "" entry in
//     Files (meaning: the book folder path IS the file). Root-level files
//     are never merged with each other or with folder books.
//
// The returned slice and each group's Files are sorted deterministically
// (book folders by string order, files by natural order) so callers get
// stable results; final on-disk play order is decided later using tag data
// where available.
func groupBooks(relPaths []string) []bookGroup {
	directAudio := map[string][]string{} // dir -> audio files directly in dir
	dirSet := map[string]bool{}          // every non-root ancestor dir of an audio file

	for _, p := range relPaths {
		d := path.Dir(p)
		directAudio[d] = append(directAudio[d], p)
		for cur := d; cur != "."; cur = path.Dir(cur) {
			dirSet[cur] = true
		}
	}

	childDirs := map[string][]string{}
	for d := range dirSet {
		parent := path.Dir(d)
		childDirs[parent] = append(childDirs[parent], d)
	}

	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	// Process shallowest directories first so a shallower book root is
	// discovered (and propagated to its descendants) before any deeper
	// directory on the same path gets a chance to claim itself as a root.
	sort.Slice(dirs, func(i, j int) bool {
		di, dj := strings.Count(dirs[i], "/"), strings.Count(dirs[j], "/")
		if di != dj {
			return di < dj
		}
		return dirs[i] < dirs[j]
	})

	// owner[d] is the book-root directory that d's audio belongs to.
	owner := map[string]string{}
	for _, d := range dirs {
		if parent := path.Dir(d); parent != "." {
			if root, ok := owner[parent]; ok {
				// An ancestor is already a book root; everything beneath it
				// (this dir included) belongs to that same book.
				owner[d] = root
				continue
			}
		}
		if len(directAudio[d]) > 0 {
			owner[d] = d
			continue
		}
		// CD/Disc/Part rollup: no audio of its own, but a subdir matching
		// the pattern leads to audio, so this dir becomes the (virtual)
		// book root for its children.
		for _, child := range childDirs[d] {
			if cdPattern.MatchString(path.Base(child)) {
				owner[d] = d
				break
			}
		}
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

	for d, files := range directAudio {
		if d == "." {
			for _, f := range files {
				addFile(f, "")
			}
			continue
		}
		root, ok := owner[d]
		if !ok {
			// Defensive: d has direct audio so the loop above always
			// assigns an owner (itself, at minimum).
			root = d
		}
		for _, f := range files {
			relToFolder := strings.TrimPrefix(f, root+"/")
			addFile(root, relToFolder)
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
