// Package identity persists the server's machine identifier and access token.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Identity is the server's stable identity, generated on first start.
type Identity struct {
	MachineID string `json:"machineIdentifier"`
	Token     string `json:"token"`
}

const fileName = "identity.json"

// Load reads the identity from dir, creating and saving a new one if none
// exists. created reports whether a new identity was generated.
func Load(dir string) (id Identity, created bool, err error) {
	path := filepath.Join(dir, fileName)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &id); err != nil {
			return Identity{}, false, fmt.Errorf("parse %s: %w", path, err)
		}
		if id.MachineID == "" || id.Token == "" {
			return Identity{}, false, fmt.Errorf("%s: missing machineIdentifier or token", path)
		}
		return id, false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return Identity{}, false, err
	}

	id = Identity{MachineID: randomHex(20), Token: randomHex(24)}
	b, err = json.MarshalIndent(id, "", "  ")
	if err != nil {
		return Identity{}, false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Identity{}, false, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return Identity{}, false, err
	}
	return id, true, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}
