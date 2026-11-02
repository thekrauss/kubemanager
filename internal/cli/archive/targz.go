package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

const dockerIgnoreFile = ".dockerignore"

// defaultIgnore : sans .dockerignore, ni dépendances ni secrets locaux ne
// partent avec le code.
var defaultIgnore = []string{"node_modules", "**/node_modules", ".env", ".env.*", "**/.env", "**/.env.*", "*.pem", "*.key", "**/*.pem", "**/*.key"}

// sensitiveFile : fichier qui ressemble à un secret, envoyé parce que le
// .dockerignore du projet ne l'exclut pas.
func sensitiveFile(rel string) bool {
	base := path.Base(rel)
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key")
}

// SourceResult : archive du code, à supprimer par l'appelant.
type SourceResult struct {
	Result
	// Sensitive : fichiers ressemblant à des secrets inclus dans l'archive.
	Sensitive []string
}

// TarGz : archive le code à construire, selon le .dockerignore (comme docker
// build). Les liens symboliques sont conservés tels quels, jamais suivis.
func TarGz(dir string) (*SourceResult, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New(i18n.T("archive.not_folder", dir))
	}
	matcher, custom, err := loadIgnore(dir)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "betorun-source-*.tar.gz")
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
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	res := &SourceResult{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && ignoredDirs[d.Name()] {
			return filepath.SkipDir
		}
		ignored, err := matcher.MatchesOrParentMatches(rel)
		if err != nil {
			return err
		}
		if ignored {
			if d.IsDir() && !matcher.Exclusions() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || ignoredFiles[d.Name()] {
			return nil
		}
		if !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		if err := addTarEntry(tw, p, rel); err != nil {
			return err
		}
		res.Files++
		if custom && sensitiveFile(rel) {
			res.Sensitive = append(res.Sensitive, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if res.Files == 0 {
		return nil, ErrEmpty
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return nil, err
	}
	if res.Size, err = tmp.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	ok = true
	res.Path = tmp.Name()
	return res, nil
}

func loadIgnore(dir string) (*patternmatcher.PatternMatcher, bool, error) {
	f, err := os.Open(filepath.Join(dir, dockerIgnoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		m, err := patternmatcher.New(defaultIgnore)
		return m, false, err
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	patterns, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	m, err := patternmatcher.New(patterns)
	return m, true, err
}

// addTarEntry : propriétaire et date neutres, pour un résultat reproductible.
func addTarEntry(tw *tar.Writer, p, rel string) error {
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	link := ""
	if info.Mode()&fs.ModeSymlink != 0 {
		if link, err = os.Readlink(p); err != nil {
			return err
		}
	}
	h, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	h.Name, h.ModTime, h.AccessTime, h.ChangeTime = rel, time.Unix(0, 0), time.Time{}, time.Time{}
	h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}
