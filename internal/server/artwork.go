package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/library"
)

var imageTypes = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png"}

// generatedThumb reports whether an item gets a frame-grab thumbnail, and
// from which part.
func (s *Server) generatedThumb(it library.Item) (library.Part, int64, bool) {
	if it.Type != library.TypeEpisode || !s.art.CanGenerate() || len(it.Media) == 0 || len(it.Media[0].Parts) == 0 {
		return library.Part{}, 0, false
	}
	return it.Media[0].Parts[0], it.Media[0].Duration, true
}

// imageURL is the URL of an item's own image of a kind, or "".
func (s *Server) imageURL(it library.Item, kind string) string {
	if img, ok := it.Images[kind]; ok {
		return fmt.Sprintf("/library/metadata/%d/%s/%d", it.ID, kind, img.ModTime)
	}
	if p, _, ok := s.generatedThumb(it); ok && kind == library.ImageThumb {
		return fmt.Sprintf("/library/metadata/%d/%s/%d", it.ID, kind, p.ModTime)
	}
	return ""
}

// imageFile resolves an item's own image file: a local image, or for an
// episode thumbnail a generated frame grab.
func (s *Server) imageFile(ctx context.Context, it library.Item, kind string) (string, bool) {
	if img, ok := it.Images[kind]; ok {
		return img.Path, true
	}
	if kind == library.ImageThumb {
		if p, dur, ok := s.generatedThumb(it); ok {
			path, err := s.art.Frame(ctx, p.ID, p.ModTime, p.File, dur)
			if err == nil {
				return path, true
			}
			s.log.Warn("frame grab failed", "item", it.ID, "err", err)
		}
	}
	return "", false
}

func serveImage(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if ct, ok := imageTypes[strings.ToLower(filepath.Ext(path))]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

func (s *Server) handleImage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		it, ok := s.item(w, r)
		if !ok {
			return
		}
		path, ok := s.imageFile(r.Context(), it, kind)
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveImage(w, r, path)
	}
}

// parseImageURL extracts the item ID and kind from an image URL of the form
// /library/metadata/{id}/{kind}[/{ts}]. Only the path is considered; the
// server never fetches remote URLs.
func parseImageURL(raw string) (id int, kind string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || len(parts) > 5 || parts[0] != "library" || parts[1] != "metadata" {
		return 0, "", false
	}
	id, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, "", false
	}
	switch parts[3] {
	case library.ImageThumb, library.ImageArt, library.ImageBanner:
		return id, parts[3], true
	}
	return 0, "", false
}

const maxTranscodeSide = 4096

func (s *Server) handlePhotoTranscode(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, kind, ok := parseImageURL(q.Get("url"))
	if !ok {
		http.Error(w, "url must be a /library/metadata/{id}/{kind} image", http.StatusBadRequest)
		return
	}
	width, errW := strconv.Atoi(q.Get("width"))
	height, errH := strconv.Atoi(q.Get("height"))
	if errW != nil || errH != nil || width < 1 || height < 1 || width > maxTranscodeSide || height > maxTranscodeSide {
		http.Error(w, "width and height must be between 1 and 4096", http.StatusBadRequest)
		return
	}
	it, err := s.store.Item(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	src, ok := s.imageFile(r.Context(), it, kind)
	if !ok {
		http.NotFound(w, r)
		return
	}
	out, err := s.art.Transcode(src, artwork.Options{
		Width: width, Height: height, Cover: q.Get("minSize") == "1", Upscale: q.Get("upscale") == "1",
	})
	switch {
	case errors.Is(err, artwork.ErrTooLarge):
		http.Error(w, "source image too large", http.StatusUnprocessableEntity)
		return
	case err != nil:
		s.log.Warn("photo transcode failed", "src", src, "err", err)
		http.NotFound(w, r)
		return
	}
	serveImage(w, r, out)
}
