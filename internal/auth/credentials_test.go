package auth

import "testing"

func TestPasswordsAndTokens(t *testing.T) {
	hash := HashPassword("a sufficiently long password")
	if !CheckPassword(hash, "a sufficiently long password") {
		t.Fatal("password did not verify")
	}
	if CheckPassword(hash, "wrong password") {
		t.Fatal("incorrect password accepted")
	}
	for _, bad := range []string{"", "plaintext", "$argon2id$v=19$m=999999999,t=3,p=2$bad$bad"} {
		if CheckPassword(bad, "password") {
			t.Fatal("malformed hash accepted")
		}
	}
	if HashPassword("same password") == HashPassword("same password") {
		t.Fatal("password salts reused")
	}
	if Secret("gtn_") == Secret("gtn_") {
		t.Fatal("tokens reused")
	}
}
