package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestPaginationLargeSize(t *testing.T) {
	f := newFixture(t, Options{})
	maxInt := strconv.Itoa(int(^uint(0) >> 1))
	for _, header := range []bool{false, true} {
		name := "query"
		if header {
			name = "header"
		}
		t.Run(name, func(t *testing.T) {
			path := f.srv.URL + "/library/sections/1/all"
			q := url.Values{"X-Plex-Container-Start": {"1"}, "X-Plex-Container-Size": {maxInt}}
			if !header {
				path += "?" + q.Encode()
			}
			r, err := http.NewRequest(http.MethodGet, path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("X-Plex-Token", testToken)
			r.Header.Set("Accept", "application/json")
			if header {
				for k := range q {
					r.Header.Set(k, q.Get(k))
				}
			}
			resp, err := f.srv.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var body struct{ MediaContainer MediaContainer }
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			mc := body.MediaContainer
			if mc.Size != 2 || mc.Offset == nil || *mc.Offset != 1 || mc.TotalSize == nil || *mc.TotalSize != 3 {
				t.Fatalf("unexpected page: %+v", mc)
			}
		})
	}
}

func FuzzPageBounds(f *testing.F) {
	maxInt := int(^uint(0) >> 1)
	for _, seed := range [][3]int{{1, maxInt, 3}, {maxInt, maxInt, 3}, {0, 0, 0}, {-1, -1, 3}, {2, 1, 3}} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, offset, size, total int) {
		if total < 0 {
			return
		}
		for _, header := range []bool{false, true} {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			q := url.Values{"X-Plex-Container-Start": {strconv.Itoa(offset)}, "X-Plex-Container-Size": {strconv.Itoa(size)}}
			if header {
				for k := range q {
					r.Header.Set(k, q.Get(k))
				}
			} else {
				r.URL.RawQuery = q.Encode()
			}
			start, end, _ := pageBounds(r, total, -1)
			if start < 0 || start > end || end > total {
				t.Fatalf("invalid bounds [%d:%d] for total=%d offset=%d size=%d", start, end, total, offset, size)
			}
		}
	})
}
