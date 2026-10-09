package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func scanSection(t *testing.T, typ string, root string) (*Store, int) {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Lib", Type: typ, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	if _, err := NewScanner(s, nil, nil).Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	return s, sec
}

func images(t *testing.T, s *Store, sec int, typ, title string) map[string]string {
	t.Helper()
	for _, it := range s.Items(sec, typ) {
		if it.Title == title {
			out := map[string]string{}
			for k, img := range it.Images {
				out[k] = filepath.Base(img.Path)
				if img.ModTime == 0 {
					t.Errorf("%s %s: zero modTime", title, k)
				}
			}
			return out
		}
	}
	t.Fatalf("%s %q not found", typ, title)
	return nil
}

func TestMovieArtwork(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"Heat (1995)/Heat (1995).mkv", "Heat (1995)/POSTER.JPG", "Heat (1995)/fanart.png",
		"Alien (1979)/Alien (1979).mp4", "Alien (1979)/Alien (1979)-poster.jpg", "Alien (1979)/folder.jpg",
		"Ronin (1998).mkv", "Ronin (1998).jpg",
		"Big (1988).mkv", "poster.jpg", // root-level poster must not apply
	} {
		writeFile(t, filepath.Join(root, f), "x")
	}
	s, sec := scanSection(t, TypeMovie, root)

	if got := images(t, s, sec, TypeMovie, "Heat"); got[ImageThumb] != "POSTER.JPG" || got[ImageArt] != "fanart.png" {
		t.Errorf("Heat = %v", got)
	}
	if got := images(t, s, sec, TypeMovie, "Alien"); got[ImageThumb] != "Alien (1979)-poster.jpg" || got[ImageArt] != "" {
		t.Errorf("Alien = %v", got)
	}
	if got := images(t, s, sec, TypeMovie, "Ronin"); got[ImageThumb] != "Ronin (1998).jpg" {
		t.Errorf("Ronin = %v", got)
	}
	if got := images(t, s, sec, TypeMovie, "Big"); len(got) != 0 {
		t.Errorf("Big picked up root artwork: %v", got)
	}
}

func TestShowArtwork(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"Lost/poster.jpg", "Lost/fanart.jpg", "Lost/banner.png",
		"Lost/season01-poster.jpg", "Lost/season-specials-poster.jpg",
		"Lost/Season 01/Lost - S01E01.mkv", "Lost/Season 01/Lost - S01E01-thumb.jpg",
		"Lost/Season 01/Lost - S01E02.mkv",
		"Lost/Season 02/Lost - S02E01.mkv", "Lost/Season 02/folder.jpg",
		"Lost/Season 03/Lost - S03E01.mkv",
		"Lost/Specials/Lost - S00E01.mkv",
		"Fargo.S01E01.mkv", "Fargo.S01E01.jpg",
	} {
		writeFile(t, filepath.Join(root, f), "x")
	}
	s, sec := scanSection(t, TypeShow, root)

	if got := images(t, s, sec, TypeShow, "Lost"); got[ImageThumb] != "poster.jpg" || got[ImageArt] != "fanart.jpg" || got[ImageBanner] != "banner.png" {
		t.Errorf("Lost = %v", got)
	}
	for title, want := range map[string]string{
		"Specials": "season-specials-poster.jpg", "Season 1": "season01-poster.jpg",
		"Season 2": "folder.jpg", "Season 3": "",
	} {
		var lostID int
		for _, sh := range s.Items(sec, TypeShow) {
			if sh.Title == "Lost" {
				lostID = sh.ID
			}
		}
		for _, sn := range s.Children(lostID) {
			if sn.Title == title {
				got := ""
				if img, ok := sn.Images[ImageThumb]; ok {
					got = filepath.Base(img.Path)
				}
				if got != want {
					t.Errorf("%s thumb = %q, want %q", title, got, want)
				}
			}
		}
	}
	eps := s.Items(sec, TypeEpisode)
	var withThumb []string
	for _, ep := range eps {
		if img, ok := ep.Images[ImageThumb]; ok {
			withThumb = append(withThumb, filepath.Base(img.Path))
		}
	}
	if len(withThumb) != 2 {
		t.Errorf("episode thumbs = %v, want S01E01-thumb and Fargo", withThumb)
	}
	// A show stored directly in the root has no show folder.
	if got := images(t, s, sec, TypeShow, "Fargo"); len(got) != 0 {
		t.Errorf("Fargo = %v", got)
	}
}

func TestArtworkSurvivesRescanAndRemoval(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Heat (1995)", "Heat (1995).mkv"), "x")
	poster := filepath.Join(root, "Heat (1995)", "poster.jpg")
	writeFile(t, poster, "x")
	s, sec := scanSection(t, TypeMovie, root)
	sc := NewScanner(s, nil, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if got := images(t, s, sec, TypeMovie, "Heat"); got[ImageThumb] != "poster.jpg" {
		t.Fatalf("after rescan: %v", got)
	}
	if err := os.Remove(poster); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if got := images(t, s, sec, TypeMovie, "Heat"); len(got) != 0 {
		t.Errorf("removed poster still set: %v", got)
	}
}
