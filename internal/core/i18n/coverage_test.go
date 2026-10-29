package i18n_test

import (
	"strings"
	"testing"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
	"github.com/thekrauss/kubemanager/internal/core/i18n/extract"
)

// TestEveryMessageIsTranslated : tout message destiné au client a sa
// traduction française et anglaise dans core/i18n/catalog/*.json. Sans ce
// test, un nouveau message s'afficherait en anglais sur un écran français
// (ou l'inverse) sans que rien ne le signale.
//
// Ajouter l'entrée dans le fichier du module concerné :
//
//	{"fr": "Site introuvable", "en": "Site not found"}
func TestEveryMessageIsTranslated(t *testing.T) {
	res, err := extract.Dir("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) < 100 {
		t.Fatalf("seulement %d messages relevés : l'extracteur ne parcourt plus le code ?", len(res.Messages))
	}
	var missing []string
	for _, m := range res.Messages {
		if !i18n.Known(m.Text) {
			missing = append(missing, m.Pos+"  "+m.Text)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d message(s) sans traduction (core/i18n/catalog/*.json) :\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
