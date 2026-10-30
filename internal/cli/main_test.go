package cli

import (
	"os"
	"testing"
)

// Jamais de navigateur pendant les tests, même lancés depuis un terminal ;
// les tests du step-up l'activent explicitement.
func TestMain(m *testing.M) {
	stepUpAllowed = func() bool { return false }
	openURL = func(string) {}
	os.Exit(m.Run())
}
