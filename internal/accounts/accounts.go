// Package accounts stores local user accounts, password hashes and issued
// tokens. See docs/spec/accounts.md.
package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// OwnerID is the account that always exists and is always an admin.
const OwnerID = 1

// MinPasswordLen is the shortest accepted password.
const MinPasswordLen = 8

// Errors returned by Store methods.
var (
	ErrNotFound       = errors.New("not found")
	ErrExists         = errors.New("name already taken")
	ErrInvalid        = errors.New("invalid")
	ErrBadCredentials = errors.New("invalid username or password")
	ErrOwner          = errors.New("the owner account cannot be deleted or demoted")
)

// Account is a local user.
type Account struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Admin        bool   `json:"admin"`
	PasswordHash string `json:"passwordHash,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
}

// HasPassword reports whether the account can log in with a password.
func (a Account) HasPassword() bool { return a.PasswordHash != "" }

// Token is an issued access token. Only its hash is kept.
type Token struct {
	ID        int    `json:"id"`
	AccountID int    `json:"accountId"`
	Hash      string `json:"hash"`
	Device    string `json:"device"`
	CreatedAt int64  `json:"createdAt"`
}

const version = 1

type data struct {
	Version       int        `json:"version"`
	NextAccountID int        `json:"nextAccountId"`
	NextTokenID   int        `json:"nextTokenId"`
	Accounts      []*Account `json:"accounts"`
	Tokens        []*Token   `json:"tokens"`
}

// Store holds accounts and tokens in memory and saves them after every
// change. It is safe for concurrent use.
type Store struct {
	mu     sync.RWMutex
	path   string
	d      data
	byHash map[string]*Token
	now    func() time.Time
}

// Open loads the store from path (in memory only if path is empty) and
// creates the owner account if needed.
func Open(path string) (*Store, error) {
	s := &Store{path: path, d: data{Version: version, NextAccountID: OwnerID}, now: time.Now}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &s.d); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			if s.d.Version != version {
				return nil, fmt.Errorf("%s: unsupported version %d", path, s.d.Version)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountLocked(OwnerID) == nil {
		s.d.Accounts = append([]*Account{{ID: OwnerID, Name: "owner", Admin: true, CreatedAt: s.now().Unix()}}, s.d.Accounts...)
		s.d.NextAccountID = max(s.d.NextAccountID, OwnerID)
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	s.reindexLocked()
	// Derive the dummy hash now so the first login for an unknown name is
	// not slower than one for a real account.
	dummyHash()
	return s, nil
}

func (s *Store) reindexLocked() {
	s.byHash = make(map[string]*Token, len(s.d.Tokens))
	for _, t := range s.d.Tokens {
		s.byHash[t.Hash] = t
	}
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(&s.d, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, b, 0o600)
}

func (s *Store) accountLocked(id int) *Account {
	for _, a := range s.d.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (s *Store) byNameLocked(name string) *Account {
	for _, a := range s.d.Accounts {
		if strings.EqualFold(a.Name, name) {
			return a
		}
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

func validName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%w: name must be 1-32 characters of letters, digits, '.', '_' or '-'", ErrInvalid)
	}
	return nil
}

func validPassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, MinPasswordLen)
	}
	return nil
}

// Accounts returns all accounts ordered by ID.
func (s *Store) Accounts() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, len(s.d.Accounts))
	for i, a := range s.d.Accounts {
		out[i] = *a
	}
	slices.SortFunc(out, func(a, b Account) int { return a.ID - b.ID })
	return out
}

// Account returns one account.
func (s *Store) Account(id int) (Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a := s.accountLocked(id); a != nil {
		return *a, nil
	}
	return Account{}, ErrNotFound
}

// Create adds an account. An empty password creates an account that can
// only be used with tokens issued by an admin.
func (s *Store) Create(name, password string, admin bool) (Account, error) {
	if err := validName(name); err != nil {
		return Account{}, err
	}
	var hash string
	if password != "" {
		if err := validPassword(password); err != nil {
			return Account{}, err
		}
		var err error
		if hash, err = hashPassword(password); err != nil {
			return Account{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byNameLocked(name) != nil {
		return Account{}, ErrExists
	}
	s.d.NextAccountID++
	a := &Account{ID: s.d.NextAccountID, Name: name, Admin: admin, PasswordHash: hash, CreatedAt: s.now().Unix()}
	s.d.Accounts = append(s.d.Accounts, a)
	return *a, s.saveLocked()
}

// Update describes changes to an account; nil fields are left alone.
type Update struct {
	Name     *string
	Password *string
	Admin    *bool
	// KeepToken is a token ID that survives a password change.
	KeepToken int
}

// Update applies u to an account. A password change revokes the account's
// tokens except u.KeepToken.
func (s *Store) Update(id int, u Update) (Account, error) {
	if u.Name != nil {
		if err := validName(*u.Name); err != nil {
			return Account{}, err
		}
	}
	var hash string
	if u.Password != nil {
		if err := validPassword(*u.Password); err != nil {
			return Account{}, err
		}
		var err error
		if hash, err = hashPassword(*u.Password); err != nil {
			return Account{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.accountLocked(id)
	if a == nil {
		return Account{}, ErrNotFound
	}
	if u.Admin != nil && id == OwnerID && !*u.Admin {
		return Account{}, ErrOwner
	}
	if u.Name != nil {
		if other := s.byNameLocked(*u.Name); other != nil && other.ID != id {
			return Account{}, ErrExists
		}
		a.Name = *u.Name
	}
	if u.Admin != nil {
		a.Admin = *u.Admin
	}
	if u.Password != nil {
		a.PasswordHash = hash
		s.d.Tokens = slices.DeleteFunc(s.d.Tokens, func(t *Token) bool {
			return t.AccountID == id && t.ID != u.KeepToken
		})
		s.reindexLocked()
	}
	return *a, s.saveLocked()
}

// Delete removes an account and its tokens. The owner cannot be deleted.
func (s *Store) Delete(id int) error {
	if id == OwnerID {
		return ErrOwner
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountLocked(id) == nil {
		return ErrNotFound
	}
	s.d.Accounts = slices.DeleteFunc(s.d.Accounts, func(a *Account) bool { return a.ID == id })
	s.d.Tokens = slices.DeleteFunc(s.d.Tokens, func(t *Token) bool { return t.AccountID == id })
	s.reindexLocked()
	return s.saveLocked()
}

// Authenticate checks a name and password. It takes the same time whether
// or not the account exists.
func (s *Store) Authenticate(name, password string) (Account, error) {
	s.mu.RLock()
	var a Account
	if p := s.byNameLocked(name); p != nil {
		a = *p
	}
	s.mu.RUnlock()

	hash := a.PasswordHash
	if hash == "" {
		hash = dummyHash()
	}
	if !checkPassword(hash, password) || !a.HasPassword() {
		return Account{}, ErrBadCredentials
	}
	return a, nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// IssueToken creates a token for an account and returns its plain value,
// which is not stored.
func (s *Store) IssueToken(accountID int, device string) (string, Token, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", Token{}, err
	}
	plain := hex.EncodeToString(b)
	device = strings.TrimSpace(device)
	if len(device) > 100 {
		device = device[:100]
	}
	if device == "" {
		device = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountLocked(accountID) == nil {
		return "", Token{}, ErrNotFound
	}
	s.d.NextTokenID++
	t := &Token{ID: s.d.NextTokenID, AccountID: accountID, Hash: hashToken(plain), Device: device, CreatedAt: s.now().Unix()}
	s.d.Tokens = append(s.d.Tokens, t)
	s.byHash[t.Hash] = t
	return plain, *t, s.saveLocked()
}

// Lookup resolves a plain token to its account.
func (s *Store) Lookup(plain string) (Account, Token, bool) {
	if plain == "" {
		return Account{}, Token{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.byHash[hashToken(plain)]
	if t == nil {
		return Account{}, Token{}, false
	}
	a := s.accountLocked(t.AccountID)
	if a == nil {
		return Account{}, Token{}, false
	}
	return *a, *t, true
}

// Tokens lists an account's tokens.
func (s *Store) Tokens(accountID int) []Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Token
	for _, t := range s.d.Tokens {
		if t.AccountID == accountID {
			out = append(out, *t)
		}
	}
	return out
}

// RevokeToken deletes a token. accountID restricts the deletion to that
// account's tokens; 0 allows any.
func (s *Store) RevokeToken(accountID, tokenID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Tokens, func(t *Token) bool {
		return t.ID == tokenID && (accountID == 0 || t.AccountID == accountID)
	})
	if i < 0 {
		return ErrNotFound
	}
	s.d.Tokens = slices.Delete(s.d.Tokens, i, i+1)
	s.reindexLocked()
	return s.saveLocked()
}
