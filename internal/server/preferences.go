package server

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/preferences"
)

// preferenceRoutes registers /:/prefs; see docs/spec/preferences.md.
func (s *Server) preferenceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /:/prefs", s.requireAdmin(s.handlePrefs))
	mux.HandleFunc("PUT /:/prefs", s.requireAdmin(s.handlePrefsPut))
	mux.HandleFunc("GET /:/prefs/get", s.requireAdmin(s.handlePrefGet))
}

// prefValue is a preference default or value: typed in JSON, and 1/0,
// decimal or text in XML.
type prefValue struct{ v any }

func (p prefValue) MarshalJSON() ([]byte, error) { return json.Marshal(p.v) }

func (p prefValue) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {
	return xml.Attr{Name: name, Value: preferences.Format(p.v)}, nil
}

// settingElem is a Setting element of a preferences response.
type settingElem struct {
	ID         string    `xml:"id,attr" json:"id"`
	Label      string    `xml:"label,attr" json:"label"`
	Summary    string    `xml:"summary,attr" json:"summary"`
	Type       string    `xml:"type,attr" json:"type"`
	Default    prefValue `xml:"default,attr" json:"default"`
	Value      prefValue `xml:"value,attr" json:"value"`
	Hidden     Bool      `xml:"hidden,attr" json:"hidden"`
	Advanced   Bool      `xml:"advanced,attr" json:"advanced"`
	Group      string    `xml:"group,attr" json:"group"`
	EnumValues string    `xml:"enumValues,attr,omitempty" json:"enumValues,omitempty"`
}

type settingsContainer struct {
	XMLName xml.Name      `xml:"MediaContainer" json:"-"`
	Size    int           `xml:"size,attr" json:"size"`
	Setting []settingElem `xml:"Setting" json:"Setting"`
}

func writeSettings(w http.ResponseWriter, r *http.Request, settings []preferences.Setting) {
	mc := settingsContainer{Size: len(settings), Setting: make([]settingElem, len(settings))}
	for i, st := range settings {
		mc.Setting[i] = settingElem{
			ID: st.ID, Label: st.Label, Summary: st.Summary, Type: string(st.Type),
			Default: prefValue{st.Default}, Value: prefValue{st.Value},
			Hidden: Bool(st.Hidden), Advanced: Bool(st.Advanced), Group: st.Group,
			EnumValues: st.EnumValues(),
		}
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			MediaContainer settingsContainer `json:"MediaContainer"`
		}{mc})
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(mc)
	_, _ = w.Write([]byte("\n"))
}

func (s *Server) handlePrefs(w http.ResponseWriter, r *http.Request) {
	writeSettings(w, r, s.prefs.All())
}

func (s *Server) handlePrefGet(w http.ResponseWriter, r *http.Request) {
	st, ok := s.prefs.Get(r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeSettings(w, r, []preferences.Setting{st})
}

// handlePrefsPut sets every query parameter except X-Plex-* client headers,
// all or none.
func (s *Server) handlePrefsPut(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid preferences query", http.StatusBadRequest)
		return
	}
	raw := map[string]string{}
	for k, vs := range query {
		if strings.HasPrefix(k, "X-Plex-") {
			continue
		}
		if len(vs) != 1 {
			http.Error(w, "preference "+k+" given more than once", http.StatusBadRequest)
			return
		}
		raw[k] = vs[0]
	}
	if len(raw) == 0 {
		http.Error(w, "no preferences given", http.StatusBadRequest)
		return
	}
	if err := s.prefs.Set(raw); err != nil {
		var unknown *preferences.UnknownError
		var invalid *preferences.InvalidError
		if errors.As(err, &unknown) || errors.As(err, &invalid) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.log.Error("save preferences", "err", err)
		http.Error(w, "could not save preferences", http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	p, _ := principalOf(r)
	s.log.Info("preferences changed", "ids", ids, "account", p.AccountID)
	for _, id := range ids {
		if st, _ := s.prefs.Get(id); st.RestartRequired {
			s.log.Info("preference change takes effect after a restart", "id", id)
		}
	}
	w.WriteHeader(http.StatusOK)
}
