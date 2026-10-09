package library

import "testing"

func TestParseEpisode(t *testing.T) {
	const root = "/tv"
	tests := []struct {
		path string
		want Episode
		ok   bool
	}{
		{"/tv/Breaking Bad/Season 01/Breaking Bad - S01E02 - Cat's in the Bag.mkv",
			Episode{Show: "Breaking Bad", Season: 1, Episode: 2, Title: "Cat's in the Bag"}, true},
		{"/tv/Doctor Who (2005)/Season 1/Doctor.Who.2005.S01E01.Rose.720p.WEB-DL.mkv",
			Episode{Show: "Doctor Who", ShowYear: 2005, Season: 1, Episode: 1, Title: "Rose"}, true},
		{"/tv/The.Office.US.S02E03.WEB.x264.mkv",
			Episode{Show: "The Office US", Season: 2, Episode: 3}, true},
		{"/tv/Friends/Season 10/Friends 10x05 The One Where.mkv",
			Episode{Show: "Friends", Season: 10, Episode: 5, Title: "The One Where"}, true},
		{"/tv/Show/Specials/Show - E01 - Pilot Outtakes.mkv",
			Episode{Show: "Show", Season: 0, Episode: 1, Title: "Pilot Outtakes"}, true},
		{"/tv/Show/Season 2/03 - Third.mkv",
			Episode{Show: "Show", Season: 2, Episode: 3, Title: "Third"}, true},
		{"/tv/Show/S01E01E02.mkv",
			Episode{Show: "Show", Season: 1, Episode: 1}, true},
		{"/tv/Show/Show s03 e07.mp4",
			Episode{Show: "Show", Season: 3, Episode: 7}, true},
		{"/tv/Show (2019) - S01E09.mkv",
			Episode{Show: "Show", ShowYear: 2019, Season: 1, Episode: 9}, true},
		{"/tv/Season 1/Lost - S01E04.mkv",
			Episode{Show: "Lost", Season: 1, Episode: 4}, true},
		{"/tv/random.mkv", Episode{}, false},
		{"/tv/Show/Season 1/1080p.mkv", Episode{}, false},
		{"/tv/Show/notes 1920x1080.mkv", Episode{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseEpisode(root, tt.path)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseEpisode(%q) = %+v, %v; want %+v, %v", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSeasonTitle(t *testing.T) {
	if SeasonTitle(0) != "Specials" || SeasonTitle(3) != "Season 3" {
		t.Error("unexpected season titles")
	}
}
