package activities

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runDetect : exécute le vrai script avec sh, sur un projet de test.
func runDetect(t *testing.T, files map[string]string) (kind, dockerfile string, ok bool) {
	t.Helper()
	ctx, gen := t.TempDir(), t.TempDir()
	for name, content := range files {
		p := filepath.Join(ctx, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	cmd := exec.Command("sh", "-c", detectScript+"\nKIND=\"$(detect_project)\" || exit 3\necho \"$KIND\"")
	cmd.Env = append(os.Environ(), "CTX="+ctx, "GEN="+gen)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", false
	}
	raw, _ := os.ReadFile(filepath.Join(gen, "Dockerfile"))
	return strings.TrimSpace(string(out)), string(raw), true
}

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		kind  string
		want  []string
	}{
		{"next", map[string]string{"package.json": `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15"}}`, "package-lock.json": "{}"}, "next",
			[]string{"npm ci", "npm run build", `"next", "start", "-p", "8080"`, "USER 1000:1000"}},
		{"next export", map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"15"}}`, "next.config.mjs": `export default { output: 'export' }`}, "next-static",
			[]string{"npm install", "nginx-unprivileged"}},
		{"next pnpm", map[string]string{"package.json": `{"scripts":{"build":"next build"},"dependencies":{"next":"15"}}`, "pnpm-lock.yaml": ""}, "next",
			[]string{"pnpm install --frozen-lockfile", "pnpm run build"}},
		{"vite", map[string]string{"package.json": `{"scripts":{"dev":"vite","build":"vite build","start":"vite"},"devDependencies":{"vite":"6"}}`, "yarn.lock": ""}, "static-build",
			[]string{"yarn install", "yarn run build", "nginx-unprivileged", "/site"}},
		{"react-scripts", map[string]string{"package.json": `{"scripts":{"start":"react-scripts start","build":"react-scripts build"},"dependencies":{"react-scripts":"5"}}`}, "static-build", nil},
		{"express", map[string]string{"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`}, "node", []string{`CMD ["npm", "start"]`, "PORT=8080"}},
		{"nuxt", map[string]string{"package.json": `{"scripts":{"build":"nuxt build","start":"nuxt start"},"dependencies":{"nuxt":"3","vite":"5"}}`}, "node", []string{"npm run build", `"npm", "start"`}},
		{"sans script", map[string]string{"package.json": `{"name":"x"}`, "server.js": ""}, "node", []string{`"node", "server.js"`}},
		{"fastapi", map[string]string{"requirements.txt": "fastapi\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"}, "fastapi", []string{`"uvicorn", "main:app"`, "pip install --no-cache-dir -r requirements.txt", `"8080"`}},
		{"fastapi sous-dossier", map[string]string{"pyproject.toml": "dependencies=['fastapi']", "app/main.py": "import fastapi\napi = fastapi.FastAPI(title='x')\n"}, "fastapi", []string{`"app.main:api"`, "pip install --no-cache-dir ."}},
		{"flask", map[string]string{"requirements.txt": "Flask==3\n", "app.py": "from flask import Flask\nserver = Flask(__name__)\n"}, "flask", []string{`"gunicorn", "app:server"`, "0.0.0.0:8080"}},
		{"django", map[string]string{"requirements.txt": "django\n", "manage.py": "os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'shop.settings')\n"}, "django", []string{`"shop.wsgi:application"`}},
		{"streamlit", map[string]string{"requirements.txt": "streamlit\n", "app.py": "import streamlit\n"}, "streamlit", []string{`"streamlit", "run", "app.py"`}},
		{"python simple", map[string]string{"main.py": "print(1)\n"}, "python", []string{`"python", "main.py"`}},
		{"statique", map[string]string{"index.html": "<h1>x</h1>"}, "static", []string{"nginx-unprivileged"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			kind, dockerfile, ok := runDetect(t, c.files)
			require.True(t, ok)
			assert.Equal(t, c.kind, kind)
			for _, w := range c.want {
				assert.Contains(t, dockerfile, w)
			}
		})
	}
}

func TestDetect_Unknown(t *testing.T) {
	_, _, ok := runDetect(t, map[string]string{"README.md": "x"})
	assert.False(t, ok)
	_, _, ok = runDetect(t, map[string]string{"package.json": `{"name":"x"}`})
	assert.False(t, ok, "rien à lancer")
}

// Le contenu d'un projet n'est jamais exécuté par la détection, même écrit
// pour s'échapper d'un guillemet.
func TestDetect_DoesNotEvaluateProjectContent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	evil := "$(touch " + marker + ")`touch " + marker + "`"
	_, dockerfile, ok := runDetect(t, map[string]string{
		"requirements.txt": "fastapi\n",
		"main.py":          "app = FastAPI(" + evil + ")\n",
		"manage.py":        "DJANGO_SETTINGS_MODULE='" + evil + ".settings'\n",
	})
	require.True(t, ok)
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "aucune commande du projet exécutée")
	assert.NotContains(t, dockerfile, "touch")
}
