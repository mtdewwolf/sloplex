package server

import "net/http"

// Provider shapes follow the shipped PMS/Web discovery contract; see
// docs/spec/providers.md. Advertise only the supported local video subset.
type MediaProvider struct {
	Identifier string            `xml:"identifier,attr" json:"identifier"`
	Title      string            `xml:"title,attr" json:"title"`
	Types      string            `xml:"types,attr" json:"types"`
	Protocols  string            `xml:"protocols,attr" json:"protocols"`
	Feature    []ProviderFeature `xml:"Feature" json:"Feature"`
}

type ProviderFeature struct {
	Key           string      `xml:"key,attr" json:"key"`
	Type          string      `xml:"type,attr" json:"type"`
	Flavor        string      `xml:"flavor,attr,omitempty" json:"flavor,omitempty"`
	ScrobbleKey   string      `xml:"scrobbleKey,attr,omitempty" json:"scrobbleKey,omitempty"`
	UnscrobbleKey string      `xml:"unscrobbleKey,attr,omitempty" json:"unscrobbleKey,omitempty"`
	Directory     []Directory `xml:"Directory" json:"Directory,omitempty"`
}

func (s *Server) handleMediaProviders(w http.ResponseWriter, r *http.Request) {
	content := ProviderFeature{Key: "/library/sections", Type: "content"}
	content.Directory = append(content.Directory, Directory{Title: "Home", HubKey: "/hubs"})
	see := s.visibility(r)
	for _, section := range s.store.Sections() {
		if !see(section.ID) {
			continue
		}
		directory := s.sectionDirectory(section)
		directory.ID = directory.Key
		directory.Key = "/library/sections/" + directory.Key
		directory.HubKey = "/hubs/sections/" + directory.ID
		directory.Location = nil
		content.Directory = append(content.Directory, directory)
	}
	writeContainer(w, r, MediaContainer{
		FriendlyName: s.prefs.FriendlyName(), MachineIdentifier: s.opts.MachineID,
		Version: s.opts.Version,
		MediaProvider: []MediaProvider{{
			Identifier: "com.plexapp.plugins.library", Title: "Library", Types: "video", Protocols: "stream,download",
			Feature: []ProviderFeature{
				content,
				{Key: "/hubs/search", Type: "search"},
				{Key: "/hubs/continueWatching", Type: "continuewatching"},
				{Key: "/library/metadata", Type: "metadata"},
				{Key: "/photo/:/transcode", Type: "imagetranscoder"},
				{Key: "/playlists", Type: "playlist", Flavor: "universal"},
				{Key: "/playQueues", Type: "playqueue", Flavor: "universal"},
				{Key: "/:/timeline", Type: "timeline", ScrobbleKey: "/:/scrobble", UnscrobbleKey: "/:/unscrobble"},
			},
		}},
	})
}
