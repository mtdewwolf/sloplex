// Package preferences keeps the server preferences exposed through /:/prefs:
// a typed registry, values stored over a config-derived default, and change
// notification for the code that applies them. See docs/spec/preferences.md.
package preferences

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Type is the wire type of a preference value.
type Type string

// Preference value types. Values are bool, int64 and string respectively.
const (
	Bool Type = "bool"
	Int  Type = "int"
	Text Type = "text"
)

// Preference IDs; see "Supported preferences" in docs/spec/preferences.md.
const (
	FriendlyName                   = "FriendlyName"
	ScheduledLibraryUpdatesEnabled = "ScheduledLibraryUpdatesEnabled"
	ScheduledLibraryUpdateInterval = "ScheduledLibraryUpdateInterval"
	FSEventLibraryUpdatesEnabled   = "FSEventLibraryUpdatesEnabled"
	GdmEnabled                     = "GdmEnabled"
	LogDebug                       = "logDebug"
)

// maxFriendlyName bounds FriendlyName in bytes.
const maxFriendlyName = 255

// defaultScanInterval is reported for the interval when the config turns
// interval scans off; it is Sloplex's built-in scanInterval.
const defaultScanInterval = 3600

// Bootstrap is what the config file and command line say. It supplies the
// defaults of preferences that have no stored value.
type Bootstrap struct {
	// FriendlyName is the name used while the FriendlyName preference is
	// blank: config friendlyName.
	FriendlyName string
	// ScanInterval is config scanInterval; 0 means interval scans are off.
	ScanInterval time.Duration
	// Watch is config watch.
	Watch bool
	// Discovery is config discovery.
	Discovery bool
	// Debug is the -debug flag.
	Debug bool
}

// Choice is one allowed value of a restricted preference.
type Choice struct {
	Value string // the value as sent on the wire
	Name  string
}

// Setting describes a preference and its current value.
type Setting struct {
	ID       string
	Label    string
	Summary  string
	Type     Type
	Group    string
	Hidden   bool
	Advanced bool
	Choices  []Choice
	// Default is the value without a stored one; Value is the current one.
	Default, Value any
	// RestartRequired says a change applies at the next start.
	RestartRequired bool
	// Consumer names what applies the value.
	Consumer string
}

// EnumValues renders Choices in the wire form "value:name|value:name".
func (s Setting) EnumValues() string {
	parts := make([]string, len(s.Choices))
	for i, c := range s.Choices {
		parts[i] = c.Value + ":" + c.Name
	}
	return strings.Join(parts, "|")
}

// def is a registry entry.
type def struct {
	Setting
	defaultOf func(Bootstrap) any
	// validate checks a value already of the right Go type.
	validate func(v any, b Bootstrap) error
}

var intervalChoices = []Choice{
	{"900", "every 15 minutes"},
	{"1800", "every 30 minutes"},
	{"3600", "hourly"},
	{"7200", "every 2 hours"},
	{"21600", "every 6 hours"},
	{"43200", "every 12 hours"},
	{"86400", "daily"},
}

// registry lists the supported preferences in wire order.
var registry = []def{
	{
		Setting: Setting{
			ID: FriendlyName, Type: Text, Group: "general",
			Label:    "Friendly name",
			Summary:  "The name that identifies this server to apps on your network. If it is blank, the name from the server's config file is used.",
			Consumer: "GET / friendlyName, GDM responses and announcements",
		},
		defaultOf: func(Bootstrap) any { return "" },
		validate: func(v any, _ Bootstrap) error {
			s := v.(string)
			if len(s) > maxFriendlyName {
				return fmt.Errorf("longer than %d bytes", maxFriendlyName)
			}
			if !utf8.ValidString(s) {
				return errors.New("not valid UTF-8")
			}
			if strings.IndexFunc(s, unicode.IsControl) >= 0 {
				return errors.New("contains control characters")
			}
			return nil
		},
	},
	{
		Setting: Setting{
			ID: ScheduledLibraryUpdatesEnabled, Type: Bool, Group: "library",
			Label:    "Scan my library periodically",
			Summary:  "Scan every library on the interval below.",
			Consumer: "interval library scans",
		},
		defaultOf: func(b Bootstrap) any { return b.ScanInterval > 0 },
	},
	{
		Setting: Setting{
			ID: ScheduledLibraryUpdateInterval, Type: Int, Group: "library",
			Label:    "Library scan interval",
			Summary:  "Seconds between scans of every library, when periodic scans are on.",
			Choices:  intervalChoices,
			Consumer: "interval library scans",
		},
		defaultOf: func(b Bootstrap) any { return bootInterval(b) },
		validate: func(v any, b Bootstrap) error {
			if inChoices(intervalChoices, v) || v.(int64) == bootInterval(b) {
				return nil
			}
			return errors.New("not one of the allowed intervals")
		},
	},
	{
		Setting: Setting{
			ID: FSEventLibraryUpdatesEnabled, Type: Bool, Group: "library",
			Label:           "Scan my library automatically",
			Summary:         "Scan a library when a change is detected in its folders. Takes effect after a restart.",
			RestartRequired: true,
			Consumer:        "library folder watching",
		},
		defaultOf: func(b Bootstrap) any { return b.Watch },
	},
	{
		Setting: Setting{
			ID: GdmEnabled, Type: Bool, Group: "network",
			Label:           "Enable local network discovery (GDM)",
			Summary:         "Let apps on the local network find this server. Takes effect after a restart.",
			RestartRequired: true,
			Consumer:        "GDM discovery",
		},
		defaultOf: func(b Bootstrap) any { return b.Discovery },
	},
	{
		Setting: Setting{
			ID: LogDebug, Type: Bool, Group: "general", Advanced: true,
			Label:    "Debug logging",
			Summary:  "Include debug information in the server log.",
			Consumer: "server log level",
		},
		defaultOf: func(b Bootstrap) any { return b.Debug },
	},
}

func bootInterval(b Bootstrap) int64 {
	if b.ScanInterval <= 0 {
		return defaultScanInterval
	}
	return int64(b.ScanInterval / time.Second)
}

func lookup(id string) (*def, bool) {
	for i := range registry {
		if registry[i].ID == id {
			return &registry[i], true
		}
	}
	return nil, false
}

// parse converts a wire value to the preference's Go type and validates it.
func (d *def) parse(raw string, b Bootstrap) (any, error) {
	var v any
	switch d.Type {
	case Bool:
		switch raw {
		case "1", "true":
			v = true
		case "0", "false":
			v = false
		default:
			return nil, errors.New("want 1, 0, true or false")
		}
	case Int:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New("want a base-10 integer")
		}
		v = n
	default:
		v = raw
	}
	return v, d.check(v, b)
}

// choice reports whether v is one of the preference's choices, or the
// preference has none.
func (d *def) choice(v any) bool {
	return len(d.Choices) == 0 || inChoices(d.Choices, v)
}

func inChoices(cs []Choice, v any) bool {
	for _, c := range cs {
		if c.Value == Format(v) {
			return true
		}
	}
	return false
}

// check validates a value of the preference's Go type.
func (d *def) check(v any, b Bootstrap) error {
	if d.validate == nil {
		return nil
	}
	return d.validate(v, b)
}

// Format renders a value in its wire text form: 1/0 for booleans, decimal
// for integers.
func Format(v any) string {
	switch v := v.(type) {
	case bool:
		if v {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return v.(string)
	}
}

// UnknownError reports an ID that is not in the registry.
type UnknownError struct{ ID string }

func (e *UnknownError) Error() string {
	return "cannot set preference value for unknown preference " + e.ID
}

// InvalidError reports a value a preference does not accept.
type InvalidError struct{ ID, Reason string }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid value for preference %s: %s", e.ID, e.Reason)
}
