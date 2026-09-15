package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

const defaultPBKDF2Iterations = 600000

func IsPasswordHash(value string) bool {
	text := strings.TrimSpace(value)
	return strings.HasPrefix(text, "pbkdf2:") || strings.HasPrefix(text, "scrypt:")
}

type CredentialState struct {
	IsDefaultCredentials      bool
	IsLegacyPlaintextPassword bool
	MustChangePassword        bool
	PasswordChangeReason      string
}

func AssessAdminCredentialState(username, storedPassword string) CredentialState {
	normalizedUsername := strings.TrimSpace(username)
	normalizedPassword := strings.TrimSpace(storedPassword)
	defaultCredentials := normalizedUsername == "admin" && VerifyAdminPassword(normalizedPassword, "admin")
	legacyPlaintext := normalizedPassword != "" && !IsPasswordHash(normalizedPassword)
	reason := ""
	switch {
	case defaultCredentials:
		reason = "default_credentials"
	case legacyPlaintext:
		reason = "legacy_plaintext_password"
	}
	return CredentialState{
		IsDefaultCredentials:      defaultCredentials,
		IsLegacyPlaintextPassword: legacyPlaintext,
		MustChangePassword:        defaultCredentials || legacyPlaintext,
		PasswordChangeReason:      reason,
	}
}

func HashPassword(password string) string {
	saltBytes := make([]byte, 16)
	_, _ = rand.Read(saltBytes)
	salt := hex.EncodeToString(saltBytes)
	rv := pbkdf2.Key([]byte(password), []byte(salt), defaultPBKDF2Iterations, 32, sha256.New)
	return "pbkdf2:sha256:" + strconv.Itoa(defaultPBKDF2Iterations) + "$" + salt + "$" + hex.EncodeToString(rv)
}

func VerifyAdminPassword(storedPassword, password string) bool {
	storedPassword = strings.TrimSpace(storedPassword)
	if storedPassword == "" || !IsPasswordHash(storedPassword) {
		return false
	}
	return CheckPasswordHash(storedPassword, password)
}

func CheckPasswordHash(pwhash, password string) bool {
	pwhash = strings.TrimSpace(pwhash)
	if pwhash == "" || password == "" || strings.Count(pwhash, "$") < 2 {
		return false
	}
	method, salt, hashval := splitHash(pwhash)
	if method == "" {
		return false
	}
	switch {
	case strings.HasPrefix(method, "pbkdf2:"):
		return checkPBKDF2(method, salt, hashval, password)
	case strings.HasPrefix(method, "scrypt:"):
		return checkScrypt(method, salt, hashval, password)
	default:
		return false
	}
}

func splitHash(pwhash string) (method, salt, hashval string) {
	i := strings.IndexByte(pwhash, '$')
	if i < 0 {
		return "", "", ""
	}
	method = pwhash[:i]
	rest := pwhash[i+1:]
	j := strings.IndexByte(rest, '$')
	if j < 0 {
		return "", "", ""
	}
	return method, rest[:j], rest[j+1:]
}

func checkPBKDF2(method, salt, hashval, password string) bool {
	parts := strings.Split(method, ":")
	if len(parts) < 2 {
		return false
	}
	algo := parts[1]
	iterations := 600000
	if len(parts) == 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			iterations = n
		}
	}
	if algo != "sha256" {
		return false
	}
	rv := pbkdf2.Key([]byte(password), []byte(salt), iterations, len(hashval)/2, sha256.New)
	return hmac.Equal([]byte(hex.EncodeToString(rv)), []byte(hashval))
}

func checkScrypt(method, salt, hashval, password string) bool {
	parts := strings.Split(method, ":")
	if len(parts) != 4 {
		return false
	}
	n, err1 := strconv.Atoi(parts[1])
	r, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	rv, err := scrypt.Key([]byte(password), []byte(salt), n, r, p, len(hashval)/2)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(hex.EncodeToString(rv)), []byte(hashval))
}
