package webauth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Accounts in these tests hash at bcrypt's minimum cost (IMP-113).
func TestMain(m *testing.M) {
	SetHashCostForTests()
	m.Run()
}

// The hook lowers the cost only inside a test binary, which this is; the
// hashes it produces still verify.
func TestSetHashCostForTests(t *testing.T) {
	if hashCost != bcrypt.MinCost {
		t.Fatalf("hashCost = %d, want bcrypt.MinCost after TestMain", hashCost)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("pw-12345678"), hashCost)
	if err != nil {
		t.Fatal(err)
	}
	if cost, _ := bcrypt.Cost(hash); cost != bcrypt.MinCost || bcrypt.CompareHashAndPassword(hash, []byte("pw-12345678")) != nil {
		t.Errorf("cost %d, or the hash does not verify", cost)
	}
}
