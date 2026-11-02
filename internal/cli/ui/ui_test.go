package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Hors terminal : aucun code de mise en forme (scripts, CI, redirections).
func TestPlainOutsideTerminal(t *testing.T) {
	t.Setenv(envForceColor, "")
	t.Setenv(envNoColor, "")
	var b bytes.Buffer
	p := New(&b)
	p.Success("déployé")
	p.Title("Déploiement de api", "nginx:1")
	p.Println(p.Status(ToneOK, "en service"), p.Link("https://api.betorun.app"))
	assert.NotContains(t, b.String(), "\x1b", "aucune séquence ANSI dans un tube")
	assert.Equal(t, "✓ déployé\n● Déploiement de api  nginx:1\n● en service https://api.betorun.app\n", b.String())
}

func TestForceAndNoColor(t *testing.T) {
	var b bytes.Buffer
	t.Setenv(envForceColor, "1")
	t.Setenv(envNoColor, "")
	New(&b).Success("ok")
	assert.Contains(t, b.String(), "\x1b[32m✓\x1b[0m", "couleurs forcées")
	assert.Contains(t, New(&b).Link("https://x"), "\x1b]8;;https://x", "lien cliquable (OSC 8)")

	b.Reset()
	t.Setenv(envNoColor, "1")
	New(&b).Success("ok")
	assert.Equal(t, "✓ ok\n", b.String(), "NO_COLOR l'emporte")
}

// Colonnes alignées sur la largeur affichée, même avec des cellules colorées.
func TestTableAlignsVisibleWidth(t *testing.T) {
	t.Setenv(envForceColor, "1")
	t.Setenv(envNoColor, "")
	var b bytes.Buffer
	p := New(&b)
	tb := p.Table("Nom", "État", "Image")
	tb.Row("web", p.Status(ToneOK, "en service"), "nginx:1.27")
	tb.Row("api-longue", p.Status(ToneError, "en erreur"), "busybox")
	assert.NoError(t, tb.Flush())

	lines := strings.Split(strings.TrimRight(ansiRe.ReplaceAllString(b.String(), ""), "\n"), "\n")
	assert.Equal(t, []string{
		"NOM         ÉTAT          IMAGE",
		"web         ● en service  nginx:1.27",
		"api-longue  ● en erreur   busybox",
	}, lines)
}

// Sans animation : une ligne par étape, rien d'effacé.
func TestStepsWithoutAnimation(t *testing.T) {
	t.Setenv(envForceColor, "")
	t.Setenv(envNoColor, "")
	var b bytes.Buffer
	s := New(&b).Steps()
	s.Start("Préparation")
	s.Start("Préparation") // même étape : rien de plus
	s.Start("Démarrage")
	s.Done()
	assert.Equal(t, "  · Préparation\n  · Démarrage\n", b.String())
}

func TestWidth(t *testing.T) {
	assert.Equal(t, 12, Width("\x1b[32m● en service\x1b[0m"))
	assert.Equal(t, 5, Width("\x1b]8;;https://x\x1b\\héllo\x1b]8;;\x1b\\"))
}
