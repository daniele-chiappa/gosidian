package v1

import (
	"testing"

	"github.com/gosidian/gosidian/internal/webauth"
)

// Accounts in these tests hash at bcrypt's minimum cost (IMP-113).
func TestMain(m *testing.M) {
	webauth.SetHashCostForTests()
	m.Run()
}
