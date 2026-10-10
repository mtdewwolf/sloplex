package server

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
)

// MediaContainer is the envelope of every API response.
// See docs/spec/http-api.md.
type MediaContainer struct {
	XMLName xml.Name `xml:"MediaContainer" json:"-"`

	Size      int  `xml:"size,attr" json:"size"`
	TotalSize *int `xml:"totalSize,attr,omitempty" json:"totalSize,omitempty"`
	Offset    *int `xml:"offset,attr,omitempty" json:"offset,omitempty"`

	Title1              string `xml:"title1,attr,omitempty" json:"title1,omitempty"`
	Title2              string `xml:"title2,attr,omitempty" json:"title2,omitempty"`
	LibrarySectionID    int    `xml:"librarySectionID,attr,omitempty" json:"librarySectionID,omitempty"`
	LibrarySectionTitle string `xml:"librarySectionTitle,attr,omitempty" json:"librarySectionTitle,omitempty"`
	ViewGroup           string `xml:"viewGroup,attr,omitempty" json:"viewGroup,omitempty"`

	FriendlyName      string `xml:"friendlyName,attr,omitempty" json:"friendlyName,omitempty"`
	MachineIdentifier string `xml:"machineIdentifier,attr,omitempty" json:"machineIdentifier,omitempty"`
	Version           string `xml:"version,attr,omitempty" json:"version,omitempty"`
	Platform          string `xml:"platform,attr,omitempty" json:"platform,omitempty"`
	PlatformVersion   string `xml:"platformVersion,attr,omitempty" json:"platformVersion,omitempty"`
	UpdatedAt         int64  `xml:"updatedAt,attr,omitempty" json:"updatedAt,omitempty"`

	Directory []Directory `xml:"Directory" json:"Directory,omitempty"`
	// Metadata elements take their XML name from Metadata.XMLName (e.g. "Video").
	Metadata []Metadata    `json:"Metadata,omitempty"`
	Account  []AccountElem `xml:"Account" json:"Account,omitempty"`
	Hub      []Hub         `xml:"Hub" json:"Hub,omitempty"`

	TranscodeSession []TranscodeSession `xml:"TranscodeSession" json:"TranscodeSession,omitempty"`
}

// TranscodeSession describes a running transcode; see
// docs/spec/transcode.md.
type TranscodeSession struct {
	Key           string `xml:"key,attr" json:"key"`
	RatingKey     string `xml:"ratingKey,attr" json:"ratingKey"`
	Protocol      string `xml:"protocol,attr" json:"protocol"`
	VideoDecision string `xml:"videoDecision,attr" json:"videoDecision"`
	AudioDecision string `xml:"audioDecision,attr" json:"audioDecision"`
	Width         int    `xml:"width,attr,omitempty" json:"width,omitempty"`
	Height        int    `xml:"height,attr,omitempty" json:"height,omitempty"`
	Complete      Bool   `xml:"complete,attr" json:"complete"`
	Throttled     Bool   `xml:"throttled,attr" json:"throttled"`

	TranscodeHwEncoding     string `xml:"transcodeHwEncoding,attr,omitempty" json:"transcodeHwEncoding,omitempty"`
	TranscodeHwFullPipeline Bool   `xml:"transcodeHwFullPipeline,attr" json:"transcodeHwFullPipeline"`
}

// Hub groups search results of one type; see docs/spec/search.md.
type Hub struct {
	HubIdentifier string `xml:"hubIdentifier,attr" json:"hubIdentifier"`
	Type          string `xml:"type,attr" json:"type"`
	Title         string `xml:"title,attr" json:"title"`
	Size          int    `xml:"size,attr" json:"size"`
	More          Bool   `xml:"more,attr" json:"more"`
	// Metadata elements take their XML name from Metadata.XMLName.
	Metadata []Metadata `json:"Metadata,omitempty"`
}

// AccountElem is an entry of GET /accounts.
type AccountElem struct {
	ID   int    `xml:"id,attr" json:"id"`
	Key  string `xml:"key,attr" json:"key"`
	Name string `xml:"name,attr" json:"name"`
}

// Bool encodes as "1"/"0" in XML attributes and true/false in JSON.
type Bool bool

// MarshalXMLAttr implements xml.MarshalerAttr.
func (b Bool) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {
	v := "0"
	if b {
		v = "1"
	}
	return xml.Attr{Name: name, Value: v}, nil
}

// Directory is a navigable entry such as a library section.
type Directory struct {
	Key        string     `xml:"key,attr" json:"key"`
	Title      string     `xml:"title,attr" json:"title"`
	Type       string     `xml:"type,attr,omitempty" json:"type,omitempty"`
	Agent      string     `xml:"agent,attr,omitempty" json:"agent,omitempty"`
	Scanner    string     `xml:"scanner,attr,omitempty" json:"scanner,omitempty"`
	Language   string     `xml:"language,attr,omitempty" json:"language,omitempty"`
	UUID       string     `xml:"uuid,attr,omitempty" json:"uuid,omitempty"`
	Refreshing *Bool      `xml:"refreshing,attr,omitempty" json:"refreshing,omitempty"`
	CreatedAt  int64      `xml:"createdAt,attr,omitempty" json:"createdAt,omitempty"`
	UpdatedAt  int64      `xml:"updatedAt,attr,omitempty" json:"updatedAt,omitempty"`
	ScannedAt  int64      `xml:"scannedAt,attr,omitempty" json:"scannedAt,omitempty"`
	Location   []Location `xml:"Location" json:"Location,omitempty"`
}

// Location is a filesystem root of a library section.
type Location struct {
	ID   int    `xml:"id,attr" json:"id"`
	Path string `xml:"path,attr" json:"path"`
}

// Metadata is a playable item. Its XML element name is set per type.
type Metadata struct {
	XMLName xml.Name `json:"-"`

	RatingKey string `xml:"ratingKey,attr" json:"ratingKey"`
	Key       string `xml:"key,attr" json:"key"`
	GUID      string `xml:"guid,attr" json:"guid"`
	Type      string `xml:"type,attr" json:"type"`
	Title     string `xml:"title,attr" json:"title"`
	TitleSort string `xml:"titleSort,attr,omitempty" json:"titleSort,omitempty"`
	Year      int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	// Index is the season or episode number; always set for those types.
	Index       *int  `xml:"index,attr,omitempty" json:"index,omitempty"`
	ParentIndex *int  `xml:"parentIndex,attr,omitempty" json:"parentIndex,omitempty"`
	Duration    int64 `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	ChildCount  int   `xml:"childCount,attr,omitempty" json:"childCount,omitempty"`
	LeafCount   int   `xml:"leafCount,attr,omitempty" json:"leafCount,omitempty"`

	// Artwork; see docs/spec/artwork.md.
	Thumb            string `xml:"thumb,attr,omitempty" json:"thumb,omitempty"`
	Art              string `xml:"art,attr,omitempty" json:"art,omitempty"`
	Banner           string `xml:"banner,attr,omitempty" json:"banner,omitempty"`
	ParentThumb      string `xml:"parentThumb,attr,omitempty" json:"parentThumb,omitempty"`
	GrandparentThumb string `xml:"grandparentThumb,attr,omitempty" json:"grandparentThumb,omitempty"`
	GrandparentArt   string `xml:"grandparentArt,attr,omitempty" json:"grandparentArt,omitempty"`

	// Play state; see docs/spec/playstate.md.
	ViewCount       int   `xml:"viewCount,attr,omitempty" json:"viewCount,omitempty"`
	ViewOffset      int64 `xml:"viewOffset,attr,omitempty" json:"viewOffset,omitempty"`
	LastViewedAt    int64 `xml:"lastViewedAt,attr,omitempty" json:"lastViewedAt,omitempty"`
	ViewedLeafCount *int  `xml:"viewedLeafCount,attr,omitempty" json:"viewedLeafCount,omitempty"`

	ParentRatingKey      string `xml:"parentRatingKey,attr,omitempty" json:"parentRatingKey,omitempty"`
	ParentKey            string `xml:"parentKey,attr,omitempty" json:"parentKey,omitempty"`
	ParentTitle          string `xml:"parentTitle,attr,omitempty" json:"parentTitle,omitempty"`
	GrandparentRatingKey string `xml:"grandparentRatingKey,attr,omitempty" json:"grandparentRatingKey,omitempty"`
	GrandparentKey       string `xml:"grandparentKey,attr,omitempty" json:"grandparentKey,omitempty"`
	GrandparentTitle     string `xml:"grandparentTitle,attr,omitempty" json:"grandparentTitle,omitempty"`

	AddedAt             int64   `xml:"addedAt,attr" json:"addedAt"`
	UpdatedAt           int64   `xml:"updatedAt,attr" json:"updatedAt"`
	LibrarySectionID    int     `xml:"librarySectionID,attr" json:"librarySectionID"`
	LibrarySectionKey   string  `xml:"librarySectionKey,attr" json:"librarySectionKey"`
	LibrarySectionTitle string  `xml:"librarySectionTitle,attr" json:"librarySectionTitle"`
	Media               []Media `xml:"Media" json:"Media,omitempty"`
}

// xmlName returns the XML element name for an item type.
func xmlName(itemType string) xml.Name {
	switch itemType {
	case "movie", "episode", "clip":
		return xml.Name{Local: "Video"}
	}
	return xml.Name{Local: "Directory"}
}

// Media is one version of an item.
type Media struct {
	ID              int    `xml:"id,attr" json:"id"`
	Duration        int64  `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	Bitrate         int    `xml:"bitrate,attr,omitempty" json:"bitrate,omitempty"`
	Width           int    `xml:"width,attr,omitempty" json:"width,omitempty"`
	Height          int    `xml:"height,attr,omitempty" json:"height,omitempty"`
	AudioChannels   int    `xml:"audioChannels,attr,omitempty" json:"audioChannels,omitempty"`
	AudioCodec      string `xml:"audioCodec,attr,omitempty" json:"audioCodec,omitempty"`
	VideoCodec      string `xml:"videoCodec,attr,omitempty" json:"videoCodec,omitempty"`
	VideoResolution string `xml:"videoResolution,attr,omitempty" json:"videoResolution,omitempty"`
	Container       string `xml:"container,attr,omitempty" json:"container,omitempty"`
	Part            []Part `xml:"Part" json:"Part"`
}

// Part is one file of a Media.
type Part struct {
	ID        int    `xml:"id,attr" json:"id"`
	Key       string `xml:"key,attr" json:"key"`
	Duration  int64  `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	File      string `xml:"file,attr" json:"file"`
	Size      int64  `xml:"size,attr" json:"size"`
	Container string `xml:"container,attr,omitempty" json:"container,omitempty"`
	// Stream is only included in item detail; see docs/spec/streams.md.
	Stream []StreamElem `xml:"Stream" json:"Stream,omitempty"`
}

// StreamElem is a video, audio or subtitle stream of a part.
type StreamElem struct {
	ID                   int    `xml:"id,attr" json:"id"`
	StreamType           int    `xml:"streamType,attr" json:"streamType"`
	Codec                string `xml:"codec,attr" json:"codec"`
	Index                *int   `xml:"index,attr,omitempty" json:"index,omitempty"`
	Language             string `xml:"language,attr,omitempty" json:"language,omitempty"`
	LanguageCode         string `xml:"languageCode,attr,omitempty" json:"languageCode,omitempty"`
	Title                string `xml:"title,attr,omitempty" json:"title,omitempty"`
	DisplayTitle         string `xml:"displayTitle,attr" json:"displayTitle"`
	ExtendedDisplayTitle string `xml:"extendedDisplayTitle,attr" json:"extendedDisplayTitle"`
	Channels             int    `xml:"channels,attr,omitempty" json:"channels,omitempty"`
	AudioChannelLayout   string `xml:"audioChannelLayout,attr,omitempty" json:"audioChannelLayout,omitempty"`
	Width                int    `xml:"width,attr,omitempty" json:"width,omitempty"`
	Height               int    `xml:"height,attr,omitempty" json:"height,omitempty"`
	Default              Bool   `xml:"default,attr,omitempty" json:"default,omitempty"`
	Forced               Bool   `xml:"forced,attr,omitempty" json:"forced,omitempty"`
	HearingImpaired      Bool   `xml:"hearingImpaired,attr,omitempty" json:"hearingImpaired,omitempty"`
	Selected             Bool   `xml:"selected,attr,omitempty" json:"selected,omitempty"`
	Key                  string `xml:"key,attr,omitempty" json:"key,omitempty"`
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// writeContainer encodes mc as XML or JSON according to the Accept header.
func writeContainer(w http.ResponseWriter, r *http.Request, mc MediaContainer) {
	mc.Size = len(mc.Directory) + len(mc.Metadata) + len(mc.Account) + len(mc.Hub) + len(mc.TranscodeSession)
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			MediaContainer MediaContainer `json:"MediaContainer"`
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
