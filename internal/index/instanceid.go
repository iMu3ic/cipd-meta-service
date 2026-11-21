package index

import (
	"encoding/base64"
	"encoding/hex"
)

// IsHexDigest reports whether s looks like a hex-encoded SHA-256 digest.
func IsHexDigest(s string) bool { return hexDigestRe.MatchString(s) }

// HexFromInstanceID converts a CIPD instance ID (SHA-256) into a hex digest.
func HexFromInstanceID(id string) (string, bool) {
	if len(id) != 44 {
		return "", false
	}
	raw, err := base64.URLEncoding.DecodeString(id)
	if err != nil || len(raw) != 33 || raw[32] != 2 {
		return "", false
	}
	return hex.EncodeToString(raw[:32]), true
}
