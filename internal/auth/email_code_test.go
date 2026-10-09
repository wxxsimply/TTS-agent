package auth

import "testing"

func TestGenerateEmailCode(t *testing.T) {
	code, err := GenerateEmailCode()
	if err != nil {
		t.Fatalf("GenerateEmailCode returned error: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected six digits, got %q", code)
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			t.Fatalf("code contains a non-digit: %q", code)
		}
	}
}

func TestHashEmailCodeNormalizesEmail(t *testing.T) {
	service := NewService("test-secret", 0, 0)
	first := service.HashEmailCode(" USER@Example.COM ", "login", "123456")
	second := service.HashEmailCode("user@example.com", "login", "123456")
	if first != second {
		t.Fatal("email hashes should use a normalized email")
	}
	if first == service.HashEmailCode("user@example.com", "login", "654321") {
		t.Fatal("different codes must have different hashes")
	}
}
