package security

import "testing"

func TestVerifyAdminPasswordRejectsPlaintextStoredPassword(t *testing.T) {
	if VerifyAdminPassword("admin", "admin") {
		t.Fatal("plain default password should not verify")
	}
	if VerifyAdminPassword("secret123", "secret123") {
		t.Fatal("plain custom password should not verify")
	}
}

func TestAssessAdminCredentialStateRecognizesHashedDefaultPassword(t *testing.T) {
	hash := HashPassword("admin")
	state := AssessAdminCredentialState("admin", hash)
	if !state.IsDefaultCredentials {
		t.Fatal("hashed admin/admin should be recognized as default credentials")
	}
	if !state.MustChangePassword {
		t.Fatal("default credentials should require password change")
	}
	if state.PasswordChangeReason != "default_credentials" {
		t.Fatalf("reason = %q, want default_credentials", state.PasswordChangeReason)
	}
}
