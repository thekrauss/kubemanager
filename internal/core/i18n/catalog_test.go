package i18n_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		l    i18n.Locale
		in   string
		want string
	}{
		{"EN→FR", i18n.FR, "permission denied", "Permission refusée"},
		{"FR→EN", i18n.EN, "site introuvable", "site not found"},
		{"même langue", i18n.EN, "permission denied", "permission denied"},
		{"format EN→FR", i18n.FR, "quota exceeded: CPU limit reached (900/500 m)", "Quota dépassé : limite de CPU atteinte (900/500 m)"},
		{"format FR→EN", i18n.EN, "trop de fichiers : 12000 (maximum 10000)", "too many files: 12000 (maximum 10000)"},
		{"sentinelle détaillée", i18n.FR, `invalid binding: unknown role "roles/nope"`, `Attribution de rôle invalide : rôle inconnu "roles/nope"`},
		{"détail générique", i18n.FR, "unknown member: user:awa@ex.sn", "Membre inconnu : user:awa@ex.sn"},
		{"inconnu inchangé", i18n.FR, "kubemanager", "kubemanager"},
		{"vide", i18n.FR, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, i18n.Translate(tc.l, tc.in))
		})
	}
}

func TestTranslate_ValidationAndBinding(t *testing.T) {
	v := "Key: 'CreateSiteRequest.Name' Error:Field validation for 'Name' failed on the 'required' tag"
	assert.Equal(t, "Name : champ obligatoire", i18n.Translate(i18n.FR, v))
	assert.Equal(t, "Name: required field", i18n.Translate(i18n.EN, v), "reformulé même en anglais")

	b := "binding error on field 'Name' of type 'CreateSiteRequest': invalid character"
	assert.Equal(t, "Champ « Name » invalide : invalid character", i18n.Translate(i18n.FR, b))
}
