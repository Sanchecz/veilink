package auth

import "testing"

func TestGenerateHashAndMatch(t *testing.T) {
	token, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(token); err != nil {
		t.Fatalf("generated token is invalid: %v", err)
	}
	hash, err := Hash(token)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	if !Match(token, parsed) {
		t.Fatal("token did not match its hash")
	}
	if Match(token+"x", parsed) {
		t.Fatal("modified token matched")
	}
}

func TestRejectMalformedTokensAndHashes(t *testing.T) {
	for _, token := range []string{"", "vl1_", "wrong_abcdefghijklmnopqrstuvwxyz", "vl1_not/base64"} {
		if err := Validate(token); err == nil {
			t.Errorf("Validate(%q) succeeded", token)
		}
	}
	for _, hash := range []string{"", "md5:00", "sha256:00", "sha256:zz"} {
		if _, err := ParseHash(hash); err == nil {
			t.Errorf("ParseHash(%q) succeeded", hash)
		}
	}
}
