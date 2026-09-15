package security

import "testing"

// 这组测试锁住一个性能前提：AssessAdminCredentialState 会在**每个后台请求**里
// 被调用（adminauth.EnsureAdminAccess → credentialState），而它内部那句
// 「是不是默认密码 admin」的判断原本每次都实打实跑一遍 60 万次 PBKDF2。
// 后台概况一打开就是十个接口并发轮询，单个请求因此被拖到秒级。
//
// 跑基准：
//   GOWORK=off go test ./pkg/security/ -run '^$' -bench Benchmark -benchtime=5x

// BenchmarkVerifyAdminPassword 是**修复前**每个后台请求都要付的代价：
// 直接验证一次 60 万次迭代的哈希。
func BenchmarkVerifyAdminPassword(b *testing.B) {
	hash := HashPassword("admin")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VerifyAdminPassword(hash, "admin")
	}
}

// BenchmarkAssessAdminCredentialState 是**修复后**的稳态代价：
// 哈希没变，结果命中缓存，只剩字符串比较。
func BenchmarkAssessAdminCredentialState(b *testing.B) {
	hash := HashPassword("admin")
	// 先跑一次把缓存焐热，测的才是稳态
	AssessAdminCredentialState("admin", hash)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AssessAdminCredentialState("admin", hash)
	}
}

// TestCredentialCacheInvalidatesOnPasswordChange 证明缓存不会给出过期结果：
// 缓存以哈希串为键，换密码必然换哈希串，键一变缓存自然失效。
func TestCredentialCacheInvalidatesOnPasswordChange(t *testing.T) {
	if got := AssessAdminCredentialState("admin", HashPassword("admin")); !got.IsDefaultCredentials {
		t.Fatal("默认密码未被识别为默认凭据")
	}
	if got := AssessAdminCredentialState("admin", HashPassword("secret123")); got.IsDefaultCredentials {
		t.Fatal("改成非默认密码后仍被判为默认凭据 —— 缓存没有随密码变化失效")
	}
	// 再改回默认密码：新的随机盐产生新哈希串，必须能重新识别出来
	if got := AssessAdminCredentialState("admin", HashPassword("admin")); !got.IsDefaultCredentials {
		t.Fatal("改回默认密码后未被识别")
	}
}

// TestCredentialStateRequiresDefaultUsername 记录一个刻意的既有行为：
// 判断条件是「用户名 == admin 且 密码 == 默认密码」，用户名一旦改成别的，
// defaultCredentials 恒为 false —— 强制改密的安全网随之失效。
// 这里把它写死成测试，避免以后有人误以为是 bug 而改掉。
func TestCredentialStateRequiresDefaultUsername(t *testing.T) {
	hash := HashPassword("admin")
	got := AssessAdminCredentialState("someone", hash)
	if got.IsDefaultCredentials {
		t.Fatal("用户名不是 admin 时不应判为默认凭据")
	}
	if got.MustChangePassword {
		t.Fatal("用户名不是 admin 时不应触发强制改密")
	}
	// 已哈希的密码也不该被当成明文旧密码
	if got.IsLegacyPlaintextPassword {
		t.Fatal("已哈希的密码不应被判为明文旧密码")
	}
}
