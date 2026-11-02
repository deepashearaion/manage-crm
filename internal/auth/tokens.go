// Package auth holds authentication logic: opaque tokens and refresh-token
// sessions. It is the start of the auth module; the legacy handlers call it.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewOpaqueToken returns a random URL-safe token (32 bytes of entropy) and
// the SHA-256 hash to store in its place.
func NewOpaqueToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken returns the SHA-256 of a token. Tokens carry 256 bits of
// entropy, so a fast unsalted hash is sufficient.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
