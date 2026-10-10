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
		// Date-based episodes.
		{"/tv/The Daily Show/The Daily Show - 2024-03-15 - Guest Name.mkv",
			Episode{Show: "The Daily Show", Season: 2024, Episode: 315, Title: "Guest Name", Aired: "2024-03-15"}, true},
		{"/tv/Colbert.2023.11.02.Jon.Stewart.720p.WEB.mkv",
			Episode{Show: "Colbert", Season: 2023, Episode: 1102, Title: "Jon Stewart", Aired: "2023-11-02"}, true},
		{"/tv/News (2010)/2019/News 01_12_2019.mp4",
			Episode{Show: "News", ShowYear: 2010, Season: 2019, Episode: 1201, Aired: "2019-12-01"}, true},
		{"/tv/Show/Season 1/Show - 1999 12 31 - Party.mkv",
			Episode{Show: "Show", Season: 1999, Episode: 1231, Title: "Party", Aired: "1999-12-31"}, true},
		// SxxEyy wins over a date.
		{"/tv/Show/Show.S02E05.2024.03.15.mkv",
			Episode{Show: "Show", Season: 2, Episode: 5, Title: "2024 03 15"}, true},
		// Not dates: no such day, part of a longer number, year out of range.
		{"/tv/Show/Show - 2024-02-30.mkv", Episode{}, false},
		{"/tv/Show/Show - 12024-03-15.mkv", Episode{}, false},
		{"/tv/Show/Show - 2024-03-150.mkv", Episode{}, false},
		{"/tv/Show/Show - 1850-03-15.mkv", Episode{}, false},
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
	if SeasonTitle(0) != "Specials" || SeasonTitle(3) != "Season 3" || SeasonTitle(2024) != "2024" {
		t.Error("unexpected season titles")
	}
}
