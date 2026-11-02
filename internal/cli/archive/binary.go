package archive

import (
	"archive/tar"
	"compress/gzip"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Déploiement d'un binaire : il est emballé avec un Dockerfile généré dans le
// même format qu'un dossier source, et construit de la même façon.

// binaryDockerfile : image minimale non root avec glibc, libstdc++, certificats
// et fuseau horaire : couvre Go, Rust et C++, statiques ou non.
const binaryDockerfile = `FROM gcr.io/distroless/cc-debian12:nonroot
COPY app /app
ENTRYPOINT ["/app"]
`

const (
	binaryEntryName     = "app"
	dockerfileEntryName = "Dockerfile"
	binaryMode          = 0o755
	dockerfileMode      = 0o644
)

// Erreurs sur le binaire fourni.
var (
	ErrNotLinuxBinary = errors.New("not a Linux executable")
	ErrWrongArch      = errors.New("wrong architecture")
)

// checkBinary : exécutable Linux amd64. Sans ce contrôle, un binaire macOS ou
// Windows ne se révélerait qu'au démarrage du conteneur.
func checkBinary(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return err
		}
		return ErrNotLinuxBinary
	}
	defer f.Close()
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return ErrNotLinuxBinary
	}
	if f.Machine != elf.EM_X86_64 {
		return fmt.Errorf("%w: %s", ErrWrongArch, strings.TrimPrefix(f.Machine.String(), "EM_"))
	}
	return nil
}

// BinaryTarGz : archive de build contenant le binaire et son Dockerfile.
func BinaryTarGz(path string) (*SourceResult, error) {
	if err := checkBinary(path); err != nil {
		return nil, err
	}
	bin, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer bin.Close()
	info, err := bin.Stat()
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "betorun-binary-*.tar.gz")
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
	entry := func(name string, mode, size int64) error {
		return tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)})
	}
	if err := entry(dockerfileEntryName, dockerfileMode, int64(len(binaryDockerfile))); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(tw, binaryDockerfile); err != nil {
		return nil, err
	}
	if err := entry(binaryEntryName, binaryMode, info.Size()); err != nil {
		return nil, err
	}
	if _, err := io.Copy(tw, bin); err != nil {
		return nil, err
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return nil, err
	}
	res := &SourceResult{}
	res.Files = 2
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
