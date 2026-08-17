package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const Prefix = "vl1_"

var ErrInvalidToken = errors.New("invalid Veilink token")

func Generate() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func Validate(token string) error {
	if !strings.HasPrefix(token, Prefix) {
		return ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, Prefix))
	if err != nil || len(raw) != 32 {
		return ErrInvalidToken
	}
	return nil
}

func Hash(token string) (string, error) {
	if err := Validate(token); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ParseHash(encoded string) ([32]byte, error) {
	var result [32]byte
	if !strings.HasPrefix(encoded, "sha256:") {
		return result, errors.New("token hash is missing the sha256 prefix")
	}
	b, err := hex.DecodeString(strings.TrimPrefix(encoded, "sha256:"))
	if err != nil || len(b) != sha256.Size {
		return result, errors.New("token hash must contain 64 hexadecimal characters")
	}
	copy(result[:], b)
	return result, nil
}

func Match(token string, expected [32]byte) bool {
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}
