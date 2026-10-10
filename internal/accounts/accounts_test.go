package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	Iterations = 1000 // keep tests fast; the format records the count
	os.Exit(m.Run())
}

func ptr[T any](v T) *T { return &v }

func TestOwnerAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Account(OwnerID)
	if err != nil || owner.Name != "owner" || !owner.Admin || owner.HasPassword() {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
	alice, err := s.Create("alice", "password1", false)
	if err != nil {
		t.Fatal(err)
	}
	plain, tok, err := s.IssueToken(alice.ID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("accounts.json mode = %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), plain) || strings.Contains(string(b), "password1") {
		t.Fatal("plain secret written to disk")
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Accounts()) != 2 {
		t.Fatalf("accounts = %+v", s2.Accounts())
	}
	a, got, ok := s2.Lookup(plain)
	if !ok || a.ID != alice.ID || got.ID != tok.ID || got.Device != "Phone" {
		t.Errorf("Lookup after reload = %+v %+v %v", a, got, ok)
	}
	if _, err := s2.Authenticate("ALICE", "password1"); err != nil {
		t.Errorf("case-insensitive login: %v", err)
	}
	// IDs are never reused.
	bob, _ := s2.Create("bob", "", false)
	if bob.ID != 3 {
		t.Errorf("bob ID = %d", bob.ID)
	}
}

func TestValidationAndRules(t *testing.T) {
	s, _ := Open("")
	for _, tc := range []struct {
		name, pw string
		want     error
	}{
		{"", "password1", ErrInvalid},
		{"has space", "password1", ErrInvalid},
		{"waytoolongnamewaytoolongnamewaytoolong", "password1", ErrInvalid},
		{"carol", "short", ErrInvalid},
		{"Owner", "password1", ErrExists},
	} {
		if _, err := s.Create(tc.name, tc.pw, false); !errors.Is(err, tc.want) {
			t.Errorf("Create(%q, %q) = %v, want %v", tc.name, tc.pw, err, tc.want)
		}
	}
	if err := s.Delete(OwnerID); !errors.Is(err, ErrOwner) {
		t.Errorf("delete owner = %v", err)
	}
	if _, err := s.Update(OwnerID, Update{Admin: ptr(false)}); !errors.Is(err, ErrOwner) {
		t.Errorf("demote owner = %v", err)
	}
	dave, _ := s.Create("dave", "", false)
	if _, err := s.Update(dave.ID, Update{Name: ptr("owner")}); !errors.Is(err, ErrExists) {
		t.Errorf("rename to taken name = %v", err)
	}
	if _, err := s.Authenticate("dave", ""); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("passwordless login = %v", err)
	}
	if _, err := s.Authenticate("nobody", "password1"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("unknown login = %v", err)
	}
}

func TestPasswordChangeRevokesOtherTokens(t *testing.T) {
	s, _ := Open("")
	a, _ := s.Create("erin", "password1", false)
	keep, keepTok, _ := s.IssueToken(a.ID, "laptop")
	drop, _, _ := s.IssueToken(a.ID, "phone")
	if _, err := s.Update(a.ID, Update{Password: ptr("password2"), KeepToken: keepTok.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Lookup(keep); !ok {
		t.Error("kept token revoked")
	}
	if _, _, ok := s.Lookup(drop); ok {
		t.Error("other token survived password change")
	}
	if _, err := s.Authenticate("erin", "password1"); err == nil {
		t.Error("old password still works")
	}
	if _, err := s.Authenticate("erin", "password2"); err != nil {
		t.Errorf("new password: %v", err)
	}

	// Revoking is scoped to the owning account unless accountID is 0.
	other, _ := s.Create("frank", "", false)
	if err := s.RevokeToken(other.ID, keepTok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account revoke = %v", err)
	}
	if err := s.RevokeToken(0, keepTok.ID); err != nil {
		t.Errorf("admin revoke = %v", err)
	}
	if n := len(s.Tokens(a.ID)); n != 0 {
		t.Errorf("tokens left = %d", n)
	}

	// Deleting an account removes its tokens.
	plain, _, _ := s.IssueToken(other.ID, "")
	if err := s.Delete(other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Lookup(plain); ok {
		t.Error("deleted account's token still valid")
	}
}

func TestHashFormat(t *testing.T) {
	h, err := hashPassword("hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(h, "hunter22") || checkPassword(h, "hunter23") {
		t.Error("check mismatch")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$x$a$b", "md5$1$a$b"} {
		if checkPassword(bad, "hunter22") {
			t.Errorf("malformed hash %q matched", bad)
		}
	}
}
