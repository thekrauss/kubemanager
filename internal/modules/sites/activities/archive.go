package activities

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

// Limits : bornes d'une archive (protection zip bomb / abus de stockage).
type Limits struct {
	MaxArchiveBytes   int64
	MaxExtractedBytes int64
	MaxFiles          int
}

// DefaultLimits : utilisées quand la config ne précise rien.
var DefaultLimits = Limits{
	MaxArchiveBytes:   100 << 20,
	MaxExtractedBytes: 500 << 20,
	MaxFiles:          10000,
}

func (l Limits) WithDefaults() Limits {
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = DefaultLimits.MaxArchiveBytes
	}
	if l.MaxExtractedBytes <= 0 {
		l.MaxExtractedBytes = DefaultLimits.MaxExtractedBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultLimits.MaxFiles
	}
	return l
}

// ArchiveError : archive refusée (contenu invalide). Non retryable : la
// rejouer donnerait le même résultat. Le message est montré à l'utilisateur.
type ArchiveError struct{ Msg string }

func (e *ArchiveError) Error() string { return e.Msg }

func archiveErrorf(format string, args ...any) error {
	return &ArchiveError{Msg: fmt.Sprintf(format, args...)}
}

// PutFunc écrit un fichier extrait dans le stockage. rel est le chemin relatif
// (sans "/" initial) à la racine du site.
type PutFunc func(ctx context.Context, rel string, r io.Reader, size int64, contentType string) error

// ExtractResult : bilan de l'extraction.
type ExtractResult struct {
	FileCount int
	SizeBytes int64
}

type archiveFile struct {
	f   *zip.File
	rel string
}

// ExtractArchive valide puis extrait une archive zip de site statique.
//
// Règles :
//   - chemins absolus, ".." ou caractères de contrôle → archive refusée (zip-slip) ;
//   - dossiers, liens symboliques, __MACOSX/, .git/ et .DS_Store ignorés ;
//   - si l'archive n'a pas d'index.html à la racine mais que tout est dans un
//     unique dossier (ex: "dist/"), ce dossier devient la racine ;
//   - index.html obligatoire à la racine (évite les mauvais dossiers uploadés) ;
//   - nombre de fichiers et taille décompressée bornés, vérifiés avant toute
//     écriture puis à la lecture (une en-tête zip peut mentir).
func ExtractArchive(ctx context.Context, zr *zip.Reader, limits Limits, put PutFunc) (ExtractResult, error) {
	limits = limits.WithDefaults()

	files, err := collectFiles(zr)
	if err != nil {
		return ExtractResult{}, err
	}
	if len(files) == 0 {
		return ExtractResult{}, archiveErrorf("l'archive est vide")
	}
	files = stripCommonRoot(files)
	if !hasRootIndex(files) {
		return ExtractResult{}, archiveErrorf("index.html introuvable à la racine de l'archive")
	}
	if len(files) > limits.MaxFiles {
		return ExtractResult{}, archiveErrorf("trop de fichiers : %d (maximum %d)", len(files), limits.MaxFiles)
	}
	var declared uint64
	for _, af := range files {
		declared += af.f.UncompressedSize64
	}
	if declared > uint64(limits.MaxExtractedBytes) {
		return ExtractResult{}, archiveErrorf("site trop volumineux une fois décompressé (maximum %d Mo)", limits.MaxExtractedBytes/domain.BytesPerMB)
	}

	var res ExtractResult
	for _, af := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		n, err := extractOne(ctx, af, limits.MaxExtractedBytes-res.SizeBytes, put)
		if err != nil {
			return res, err
		}
		res.FileCount++
		res.SizeBytes += n
	}
	return res, nil
}

func collectFiles(zr *zip.Reader) ([]archiveFile, error) {
	var out []archiveFile
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		if f.Mode().Type() != 0 { // lien symbolique ou fichier spécial
			continue
		}
		if strings.ContainsAny(name, "\x00\r\n") || strings.HasPrefix(name, "/") || path.IsAbs(name) {
			return nil, archiveErrorf("chemin interdit dans l'archive : %q", f.Name)
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return nil, archiveErrorf("chemin interdit dans l'archive : %q", f.Name)
			}
		}
		rel := strings.TrimPrefix(path.Clean(name), "./")
		if ignored(rel) {
			continue
		}
		out = append(out, archiveFile{f: f, rel: rel})
	}
	return out, nil
}

func ignored(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if ignoredArchiveEntries[seg] {
			return true
		}
	}
	return false
}

func hasRootIndex(files []archiveFile) bool {
	for _, af := range files {
		if af.rel == domain.IndexFile {
			return true
		}
	}
	return false
}

// stripCommonRoot : "dist/index.html", "dist/app.js" → "index.html", "app.js".
func stripCommonRoot(files []archiveFile) []archiveFile {
	if hasRootIndex(files) {
		return files
	}
	root := ""
	for _, af := range files {
		first, _, ok := strings.Cut(af.rel, "/")
		if !ok {
			return files // un fichier à la racine : pas de dossier commun
		}
		if root == "" {
			root = first
		} else if first != root {
			return files
		}
	}
	out := make([]archiveFile, len(files))
	for i, af := range files {
		out[i] = archiveFile{f: af.f, rel: strings.TrimPrefix(af.rel, root+"/")}
	}
	return out
}

func extractOne(ctx context.Context, af archiveFile, budget int64, put PutFunc) (int64, error) {
	size := int64(af.f.UncompressedSize64)
	if size > budget {
		return 0, archiveErrorf("site trop volumineux une fois décompressé")
	}
	rc, err := af.f.Open()
	if err != nil {
		return 0, archiveErrorf("fichier illisible dans l'archive : %s", af.rel)
	}
	defer rc.Close()

	// La taille annoncée sert de borne stricte : un flux plus long est refusé.
	counted := &countingReader{r: io.LimitReader(rc, size+1)}
	if err := put(ctx, af.rel, counted, size, contentTypeFor(af.rel)); err != nil {
		var ae *ArchiveError
		if errors.As(err, &ae) || errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) {
			return 0, archiveErrorf("fichier corrompu dans l'archive : %s", af.rel)
		}
		return 0, err
	}
	if counted.n != size {
		return 0, archiveErrorf("fichier corrompu dans l'archive : %s", af.rel)
	}
	return size, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// contentTypes : complète la table de mime (absente ou partielle selon l'image).
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
	".pdf":         "application/pdf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
}

func contentTypeFor(rel string) string {
	ext := strings.ToLower(path.Ext(rel))
	if ct, ok := contentTypes[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return httpheader.MIMEOctetStream
}
