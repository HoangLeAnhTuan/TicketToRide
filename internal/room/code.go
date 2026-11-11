package room

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// Alphabet excludes visually ambiguous characters: 0, 1, I, O.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var (
	codeRegex          = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)
	ErrInvalidRoomCode = errors.New("room code must be 6 characters from alphabet A-HJ-NP-Z2-9")
)

// GenerateRoomCode generates a cryptographically random 6-character room code.
func GenerateRoomCode() (string, error) {
	b := make([]byte, 6)
	alphabetLen := big.NewInt(int64(len(codeAlphabet)))
	for i := 0; i < 6; i++ {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		b[i] = codeAlphabet[idx.Int64()]
	}
	return string(b), nil
}

// NormalizeRoomCode uppercases, trims, and validates a room code.
func NormalizeRoomCode(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if !codeRegex.MatchString(normalized) {
		return "", ErrInvalidRoomCode
	}
	return normalized, nil
}
