package library

import "testing"

func TestParseMovie(t *testing.T) {
	tests := []struct {
		path  string
		title string
		year  int
	}{
		{"/m/The Matrix (1999).mkv", "The Matrix", 1999},
		{"/m/The.Matrix.1999.1080p.BluRay.x264.mkv", "The Matrix", 1999},
		{"/m/2001 A Space Odyssey (1968).mp4", "2001 A Space Odyssey", 1968},
		{"/m/1917 (2019).mkv", "1917", 2019},
		{"/m/1917.mkv", "1917", 0},
		{"/m/Mr. Smith Goes to Washington (1939).avi", "Mr. Smith Goes to Washington", 1939},
		{"/m/Blade Runner 2049 (2017).mkv", "Blade Runner 2049", 2017},
		{"/m/Heat (1995)/movie.mkv", "Heat", 1995},
		{"/m/Heat (1995)/Heat.mkv", "Heat", 1995},
		{"/m/Some_Movie_[2004].mkv", "Some Movie", 2004},
		{"/m/Untitled.Film.720p.WEB-DL.mkv", "Untitled Film", 0},
		{"/m/Alien - 1979.mkv", "Alien", 1979},
		// A genre before the year or release tags is dropped; numbering is
		// left to the scanner, which can see the folder.
		{"/m/01 Halloween - Horror 1978 Donald Pleasence Eng Subs 720p [H264-mp4].mp4", "01 Halloween", 1978},
		{"/m/Pulp Fiction - CRIME (1994).mkv", "Pulp Fiction", 1994},
		{"/m/Some Film - Sci-Fi 720p.mkv", "Some Film", 0},
		{"/m/Thriller (1973).mkv", "Thriller", 1973},
		{"/m/The War (2007).mkv", "The War", 2007},
		{"/m/Mission Impossible - Fallout (2018).mkv", "Mission Impossible - Fallout", 2018},
	}
	for _, tt := range tests {
		title, year := ParseMovie(tt.path)
		if title != tt.title || year != tt.year {
			t.Errorf("ParseMovie(%q) = %q, %d; want %q, %d", tt.path, title, year, tt.title, tt.year)
		}
	}
}

func TestSortTitle(t *testing.T) {
	for in, want := range map[string]string{
		"The Matrix":       "matrix",
		"A Quiet Place":    "quiet place",
		"An American Tail": "american tail",
		"Theory":           "theory",
		"The":              "the",
	} {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkip(t *testing.T) {
	for name, want := range map[string]bool{
		"Movie (2000).mkv":         false,
		"movie-sample.mkv":         true,
		"Sample.mkv":               true,
		"Samples Of Life.mkv":      false,
		"Movie (2000)-trailer.mp4": true,
	} {
		if got := skipFile(name); got != want {
			t.Errorf("skipFile(%q) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{
		"Extras": true, "Behind The Scenes": true, ".git": true, "Heat (1995)": false,
	} {
		if got := skipDir(name); got != want {
			t.Errorf("skipDir(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDropNumbering(t *testing.T) {
	files := []ScannedFile{
		{Path: "/m/Halloween/01 a.mp4", Title: "01 Halloween"},
		{Path: "/m/Halloween/02 b.mp4", Title: "02 - Halloween II"},
		{Path: "/m/Halloween/10 c.mp4", Title: "10. Halloween II"},
		{Path: "/m/Halloween/x.mp4", Title: "Halloween Bonus"},
		{Path: "/m/Jump/21.mkv", Title: "21 Jump Street"},
		{Path: "/m/Jump/22.mkv", Title: "22 Jump Street"},
		{Path: "/m/10 Cloverfield Lane (2016).mkv", Title: "10 Cloverfield Lane"},
		{Path: "/m/Alone/01 Solo.mkv", Title: "01 Solo"},
		{Path: "/m/Nums/01.mkv", Title: "01"},
		{Path: "/m/Nums/02.mkv", Title: "02 1917"},
		{Path: "/m/Nums/03.mkv", Title: "03 Dunkirk"},
		{Path: "/m/Lone/02 1917.mkv", Title: "02 1917"},
		{Path: "/m/Lone/1941.mkv", Title: "1941"},
	}
	dropNumbering(files)
	want := []string{"Halloween", "Halloween II", "Halloween II", "Halloween Bonus",
		"21 Jump Street", "22 Jump Street", "10 Cloverfield Lane", "01 Solo", "01", "1917", "Dunkirk", "02 1917", "1941"}
	for i, f := range files {
		if f.Title != want[i] {
			t.Errorf("%s: %q, want %q", f.Path, f.Title, want[i])
		}
	}
}
