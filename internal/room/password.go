package room

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrPasswordTooLong = errors.New("room password exceeds maximum length of 72 bytes")
)

// HashPassword hashes a room password using bcrypt with standard cost.
func HashPassword(password string) (string, error) {
	return hashPassword(password, bcrypt.DefaultCost)
}

func hashPassword(password string, cost int) (string, error) {
	if len(password) == 0 {
		return "", nil
	}
	if len(password) > 72 {
		return "", ErrPasswordTooLong
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// VerifyPassword compares a plaintext password against a bcrypt hash.
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		// Room has no password
		return true
	}
	if len(password) == 0 || len(password) > 72 {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
