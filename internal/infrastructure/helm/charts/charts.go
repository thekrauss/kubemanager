// Package charts : charts Helm des workloads, embarqués dans le binaire (le
// worker n'a besoin d'aucun fichier sur le disque, en image comme en natif).
package charts

import (
	"embed"
	"io/fs"
	"path"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// StandardAppName : nom du chart des conteneurs (et de son répertoire
// embarqué ; la directive go:embed ci-dessous doit rester littérale).
const StandardAppName = "standard-app"

//go:embed all:standard-app
var files embed.FS

// StandardApp : chart des conteneurs (standard-app).
func StandardApp() (*chart.Chart, error) {
	return load(StandardAppName)
}

func load(dir string) (*chart.Chart, error) {
	var buffered []*loader.BufferedFile
	err := fs.WalkDir(files, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		rel := p[len(dir)+1:]
		buffered = append(buffered, &loader.BufferedFile{Name: path.Clean(rel), Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loader.LoadFiles(buffered)
}
