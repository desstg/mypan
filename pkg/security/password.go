package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

const defaultPBKDF2Iterations = 600000

// defaultPasswordCache 缓存「这个存储值是不是默认密码 admin」的判断结果。
//
// 为什么要缓存：一次 PBKDF2 是 60 万次迭代（见 defaultPBKDF2Iterations），
// 在普通 CPU 上验证一次要几百毫秒。而 AssessAdminCredentialState 会在
// **每一个后台请求**里被调用一次（adminauth.EnsureAdminAccess → credentialState，
// 用来判断要不要强制改密），后台概况一打开就是十个接口并发轮询 ——
// 于是每个请求都白白烧掉一遍哈希，单请求被拖到秒级。
//
// 缓存是安全的：键就是被验证的输入本身。密码一改，HashPassword 会生成新的
// 随机盐、哈希串随之改变，键也就变了，缓存自然失效 —— 不存在过期不一致的窗口，
// 也不需要任何显式清除逻辑。条目数等于改密次数，实际只有一条。
var defaultPasswordCache sync.Map

// matchesDefaultPassword 判断存储的凭据是不是默认密码 admin。
// 明文、空串等情况 VerifyAdminPassword 本身会立刻返回 false，走缓存同样正确。
func matchesDefaultPassword(storedPassword string) bool {
	if cached, ok := defaultPasswordCache.Load(storedPassword); ok {
		return cached.(bool)
	}
	result := VerifyAdminPassword(storedPassword, "admin")
	defaultPasswordCache.Store(storedPassword, result)
	return result
}

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
	defaultCredentials := normalizedUsername == "admin" && matchesDefaultPassword(normalizedPassword)
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
