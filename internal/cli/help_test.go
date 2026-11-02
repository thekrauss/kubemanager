package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Aide traduite : commandes rangées par usage, exemples, options typées.
func TestHelp_GroupedAndTranslated(t *testing.T) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_API_URL", "https://api.betorun.app/kmanager/v1")
	t.Setenv("BETORUN_LANG", "fr")

	out, err := run(t)
	require.NoError(t, err)
	for _, want := range []string{"Premiers pas", "DÉMARRER", "DÉPLOYER", "ACCÈS ET SÉCURITÉ", "AUTRES COMMANDES",
		"workloads (wl, containers)", "help         Aide d'une commande", "https://api.betorun.app/docs"} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "Available Commands")
	assert.NotContains(t, out, "\x1b", "pas de couleurs hors terminal")

	out, err = run(t, "help", "workloads", "logs")
	require.NoError(t, err)
	assert.Contains(t, out, "betorun workloads logs <nom> [options]")
	assert.Contains(t, out, "--tail nombre")
	assert.Contains(t, out, "(par défaut : 200)")
	assert.Contains(t, out, "# journaux d'avant le dernier redémarrage")
	assert.NotContains(t, out, "-h, --help", "l'option d'aide n'est pas listée")

	t.Setenv("BETORUN_LANG", "en")
	out, err = run(t, "sites", "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "COMMANDS")
	assert.Contains(t, out, "# attach your domain")
}

// Fautes de frappe : erreur traduite, suggestion, renvoi vers l'aide.
func TestHelp_InputErrors(t *testing.T) {
	t.Setenv("BETORUN_CONFIG_DIR", t.TempDir())
	t.Setenv("BETORUN_LANG", "fr")

	_, err := run(t, "wokloads")
	require.Error(t, err)
	assert.Equal(t, "commande inconnue « wokloads » pour « betorun »\n  Vouliez-vous dire : workloads ?\n  Aide : betorun --help", err.Error())

	_, err = run(t, "workloads", "stat")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Vouliez-vous dire : start, status, stop ?")

	_, err = run(t, "workloads", "logs")
	require.Error(t, err)
	assert.Equal(t, "arguments incorrects, attendu : betorun workloads logs <nom> [options]\n  Aide : betorun workloads logs --help", err.Error())

	_, err = run(t, "workloads", "list", "--foo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "option inconnue : --foo")

	_, err = run(t, "help", "nimporte")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commande inconnue « nimporte »")
}
