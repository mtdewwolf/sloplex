package accounts

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Iterations is the PBKDF2 work factor for new hashes. Tests lower it.
var Iterations = 600_000

const (
	saltLen = 16
	keyLen  = 32
	scheme  = "pbkdf2-sha256"
)

var b64 = base64.RawStdEncoding

// hashPassword returns "pbkdf2-sha256$<iterations>$<salt>$<key>".
func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, Iterations, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", scheme, Iterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// checkPassword reports whether password matches encoded. A malformed or
// empty encoded hash never matches.
func checkPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := b64.DecodeString(parts[2])
	want, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked against when an account does not exist, so that
// login timing does not reveal which names exist.
var dummyHash = sync.OnceValue(func() string {
	h, err := hashPassword("sloplex-dummy-password")
	if err != nil {
		panic(err)
	}
	return h
})
