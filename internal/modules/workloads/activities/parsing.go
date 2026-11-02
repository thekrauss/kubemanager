package activities

import (
	"fmt"
	"strings"
)

// ImageInfo : repository + tag séparés d'une image "foo/bar:1.2.3".
type ImageInfo struct {
	Repository string
	Tag        string
}

// ParseImage : standalone (pas une method) pour pouvoir l'appeler en toute
// sécurité depuis workflow.ExecuteLocalActivity sans se prendre un nil deref
// sur un receiver non initialisé (fix A2).
//
// Gère :
//   - "nginx"              → Repository=nginx, Tag=latest
//   - "nginx:1.21"         → Repository=nginx, Tag=1.21
//   - "ghcr.io/org/app:v2" → Repository=ghcr.io/org/app, Tag=v2
//   - "nginx@sha256:abc..." → Repository=nginx, Tag=@sha256:abc... (digest preserved)
func ParseImage(fullImage string) (ImageInfo, error) {
	fullImage = strings.TrimSpace(fullImage)
	if fullImage == "" {
		return ImageInfo{}, fmt.Errorf("image cannot be empty")
	}

	// Digest pinning : nginx@sha256:abc...
	if idx := strings.Index(fullImage, "@"); idx > 0 {
		return ImageInfo{
			Repository: fullImage[:idx],
			Tag:        fullImage[idx:], // conserve le "@sha256:..."
		}, nil
	}

	// Tag classique — on split sur le DERNIER ":" car le registry peut
	// contenir un ":" (ex: "localhost:5000/app:v1").
	if idx := strings.LastIndex(fullImage, ":"); idx > 0 {
		// Garde-fou : si c'est un port registry (ex: "localhost:5000"),
		// pas un tag — vérifier la présence de "/" après.
		if slash := strings.Index(fullImage[idx:], "/"); slash == -1 {
			return ImageInfo{
				Repository: fullImage[:idx],
				Tag:        fullImage[idx+1:],
			}, nil
		}
	}

	// Pas de tag → default "latest" (standard Docker).
	return ImageInfo{Repository: fullImage, Tag: defaultImageTag}, nil
}
