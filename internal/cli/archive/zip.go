// Package archive : préparation du dossier à publier (.zip).
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// Ignorés : métadonnées de gestion de version et fichiers parasites des OS.
var ignoredDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}
var ignoredFiles = map[string]bool{".DS_Store": true, "Thumbs.db": true, "desktop.ini": true}

var ErrEmpty = errors.New("the folder contains no file to publish")

// Result : archive écrite dans un fichier temporaire (à supprimer par l'appelant).
type Result struct {
	Path  string
	Files int
	Size  int64
}

// Zip : archive les fichiers ordinaires de dir (ordre stable, chemins
// relatifs). Les liens symboliques ne sont jamais suivis : ils pourraient
// pointer hors du dossier.
func Zip(dir string) (*Result, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New(i18n.T("archive.not_folder", dir))
	}
	var files []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ignoredFiles[d.Name()] {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrEmpty
	}
	sort.Strings(files)

	tmp, err := os.CreateTemp("", "betorun-site-*.zip")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	zw := zip.NewWriter(tmp)
	for _, path := range files {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, err
		}
		if err := addFile(zw, path, filepath.ToSlash(rel)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	size, err := tmp.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	ok = true
	return &Result{Path: tmp.Name(), Files: len(files), Size: size}, nil
}

func addFile(zw *zip.Writer, path, name string) error {
	if strings.HasPrefix(name, "../") {
		return fmt.Errorf("unexpected path %s", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// HumanSize : taille lisible, unités de la langue de l'interface.
func HumanSize(n int64) string {
	units := [3]string{"o", "Ko", "Mo"}
	if i18n.Lang() == i18n.LangEN {
		units = [3]string{"B", "KB", "MB"}
	}
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f %s", float64(n)/(1<<20), units[2])
	case n >= 1<<10:
		return fmt.Sprintf("%.1f %s", float64(n)/(1<<10), units[1])
	}
	return fmt.Sprintf("%d %s", n, units[0])
}
