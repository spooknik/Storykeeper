package library

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

type chapterRow struct {
	Title   string
	StartMs int64
	EndMs   int64
}

type ffprobeChapter struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Tags      struct {
		Title string `json:"title"`
	} `json:"tags"`
}

type ffprobeChaptersOutput struct {
	Chapters []ffprobeChapter `json:"chapters"`
}

func ffprobeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

func readFFprobeChapters(ctx context.Context, path string) ([]ffprobeChapter, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_chapters", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var res ffprobeChaptersOutput
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	return res.Chapters, nil
}

// buildChapters returns chapters on the book's virtual timeline (sum of
// preceding file durations + offset within the file). It reads embedded
// chapters via ffprobe when available; if the book ends up with none and
// has more than one file, it synthesizes one chapter per file.
func buildChapters(ctx context.Context, files []*fileMeta, haveFFprobe bool) []chapterRow {
	var chapters []chapterRow
	if haveFFprobe {
		var offset int64
		for _, f := range files {
			raw, err := readFFprobeChapters(ctx, f.absPath)
			if err == nil {
				for _, c := range raw {
					sec1, err1 := strconv.ParseFloat(c.StartTime, 64)
					sec2, err2 := strconv.ParseFloat(c.EndTime, 64)
					if err1 != nil || err2 != nil {
						continue
					}
					title := strings.TrimSpace(c.Tags.Title)
					chapters = append(chapters, chapterRow{
						Title:   title,
						StartMs: offset + int64(math.Round(sec1*1000)),
						EndMs:   offset + int64(math.Round(sec2*1000)),
					})
				}
			}
			offset += f.durMs
		}
	}

	if len(chapters) == 0 && len(files) > 1 {
		chapters = chapters[:0]
		var offset int64
		for _, f := range files {
			title := firstTag(f.tags, taglib.Title)
			if title == "" {
				base := filepath.Base(f.relPath)
				title = strings.TrimSuffix(base, filepath.Ext(base))
			}
			chapters = append(chapters, chapterRow{
				Title:   title,
				StartMs: offset,
				EndMs:   offset + f.durMs,
			})
			offset += f.durMs
		}
	}

	for i := range chapters {
		if chapters[i].Title == "" {
			chapters[i].Title = fmt.Sprintf("Chapter %d", i+1)
		}
	}

	return chapters
}
