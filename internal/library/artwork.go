package library

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// imageExts are the artwork file extensions, in preference order.
var imageExts = []string{".jpg", ".jpeg", ".png"}

// dirCache lists directories once per artwork pass.
type dirCache map[string]map[string]string // dir -> lower-case name -> real name

func (c dirCache) files(dir string) map[string]string {
	if m, ok := c[dir]; ok {
		return m
	}
	m := map[string]string{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				m[strings.ToLower(e.Name())] = e.Name()
			}
		}
	}
	c[dir] = m
	return m
}

// find returns the first existing image named base+ext in dir, or nil.
func (c dirCache) find(dir string, bases ...string) *Image {
	if dir == "" {
		return nil
	}
	files := c.files(dir)
	for _, b := range bases {
		for _, ext := range imageExts {
			name, ok := files[strings.ToLower(b+ext)]
			if !ok {
				continue
			}
			path := filepath.Join(dir, name)
			if fi, err := os.Stat(path); err == nil {
				return &Image{Path: path, ModTime: fi.ModTime().Unix()}
			}
		}
	}
	return nil
}

func stem(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// firstFile returns the first part's file of an item, if any.
func firstFile(it Item) string {
	if len(it.Media) > 0 && len(it.Media[0].Parts) > 0 {
		return it.Media[0].Parts[0].File
	}
	return ""
}

// findArtwork resolves local artwork for every item of a section, following
// docs/spec/artwork.md.
func findArtwork(sec Section, store *Store) map[int]map[string]Image {
	c := dirCache{}
	out := map[int]map[string]Image{}
	set := func(id int, kind string, img *Image) {
		if img == nil {
			return
		}
		if out[id] == nil {
			out[id] = map[string]Image{}
		}
		out[id][kind] = *img
	}
	isRoot := func(dir string) bool {
		for _, r := range sec.Paths {
			if filepath.Clean(r) == filepath.Clean(dir) {
				return true
			}
		}
		return false
	}

	switch sec.Type {
	case TypeMovie:
		for _, it := range store.Items(sec.ID, TypeMovie) {
			file := firstFile(it)
			if file == "" {
				continue
			}
			dir, st := filepath.Dir(file), stem(file)
			thumb := []string{st + "-poster", st}
			art := []string{st + "-fanart"}
			if !isRoot(dir) {
				thumb = append(thumb, "poster", "folder", "cover", "movie")
				art = append(art, "fanart", "background", "art", "backdrop")
			}
			set(it.ID, ImageThumb, c.find(dir, thumb...))
			set(it.ID, ImageArt, c.find(dir, art...))
		}

	case TypeShow:
		for _, show := range store.Items(sec.ID, TypeShow) {
			showDir := ""
			if leaves := store.Leaves(show.ID); len(leaves) > 0 {
				showDir = showFolder(sec.Paths, firstFile(leaves[0]))
			}
			set(show.ID, ImageThumb, c.find(showDir, "poster", "folder", "show", "cover"))
			set(show.ID, ImageArt, c.find(showDir, "fanart", "background", "art", "backdrop"))
			set(show.ID, ImageBanner, c.find(showDir, "banner"))

			for _, season := range store.Children(show.ID) {
				n := season.Index
				names := []string{fmt.Sprintf("season%02d-poster", n), fmt.Sprintf("season%02d", n),
					fmt.Sprintf("season%d-poster", n), fmt.Sprintf("season%d", n)}
				if n == 0 {
					names = append(names, "season-specials-poster", "season-specials")
				}
				img := c.find(showDir, names...)
				if img == nil {
					if eps := store.Children(season.ID); len(eps) > 0 {
						if dir := filepath.Dir(firstFile(eps[0])); isSeasonFolder(dir) {
							img = c.find(dir, "poster", "folder", "cover", "season")
						}
					}
				}
				set(season.ID, ImageThumb, img)
			}
		}
		for _, ep := range store.Items(sec.ID, TypeEpisode) {
			if file := firstFile(ep); file != "" {
				st := stem(file)
				set(ep.ID, ImageThumb, c.find(filepath.Dir(file), st+"-thumb", st))
			}
		}
	}
	return out
}

func isSeasonFolder(dir string) bool {
	_, ok := seasonDir(filepath.Base(dir))
	return ok
}

// showFolder returns the show folder of an episode file, or "" if the file
// sits directly in a library root.
func showFolder(roots []string, file string) string {
	for _, root := range roots {
		rel, err := filepath.Rel(root, file)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return ""
		}
		if _, isSeason := seasonDir(parts[0]); isSeason {
			return filepath.Clean(root)
		}
		return filepath.Join(root, parts[0])
	}
	return ""
}
