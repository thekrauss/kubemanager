package extract

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDump écrit l'inventaire si I18N_DUMP est posé (outil, pas une assertion).
func TestDump(t *testing.T) {
	out := os.Getenv("I18N_DUMP")
	if out == "" {
		t.Skip("I18N_DUMP non posé")
	}
	res, err := Dir("../../..")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d messages, %d non vérifiables", len(res.Messages), len(res.Unresolved))
}
