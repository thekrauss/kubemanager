// Package objectstore : accès S3-compatible pour les fichiers des sites
// statiques (SeaweedFS en dev, S3 managé en prod ; client minio-go, compatible
// avec tout fournisseur S3). Partagé par l'API (upload), le worker Temporal
// (extraction) et le binaire static-edge (lecture seule).
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound : l'objet demandé n'existe pas.
var ErrNotFound = errors.New("object not found")

// Object : contenu lisible + seekable (http.ServeContent gère les Range).
type Object interface {
	io.ReadSeekCloser
}

// ObjectInfo : métadonnées d'un objet.
type ObjectInfo struct {
	Size         int64
	ETag         string
	ContentType  string
	LastModified time.Time
}

type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get ouvre un objet ; ErrNotFound s'il n'existe pas.
	Get(ctx context.Context, key string) (Object, ObjectInfo, error)
	// Download copie un objet dans w (fichier temporaire typiquement).
	Download(ctx context.Context, key string, w io.Writer) error
	Delete(ctx context.Context, key string) error
	// DeletePrefix supprime tous les objets sous prefix.
	DeletePrefix(ctx context.Context, prefix string) error
}

// Config : volontairement indépendante de configs.GlobalConfig pour que le
// binaire static-edge n'embarque pas la config complète de la plateforme.
type Config struct {
	Endpoint  string // host:port, sans schéma
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

type minioStore struct {
	client *minio.Client
	bucket string
}

// New construit un Store MinIO. Ne crée pas le bucket : voir EnsureBucket.
func New(cfg Config) (Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("object store: endpoint and bucket are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("object store client: %w", err)
	}
	return &minioStore{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket crée le bucket s'il n'existe pas (nécessite des droits admin :
// à appeler côté API/worker, jamais côté edge).
func EnsureBucket(ctx context.Context, s Store, region string) error {
	ms, ok := s.(*minioStore)
	if !ok {
		return nil
	}
	exists, err := ms.client.BucketExists(ctx, ms.bucket)
	if err != nil {
		return fmt.Errorf("bucket exists: %w", err)
	}
	if exists {
		return nil
	}
	return ms.client.MakeBucket(ctx, ms.bucket, minio.MakeBucketOptions{Region: region})
}

func (s *minioStore) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *minioStore) Get(ctx context.Context, key string) (Object, ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, mapErr(err)
	}
	// GetObject est paresseux : Stat déclenche la requête et révèle un 404.
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, ObjectInfo{}, mapErr(err)
	}
	return obj, ObjectInfo{
		Size:         st.Size,
		ETag:         st.ETag,
		ContentType:  st.ContentType,
		LastModified: st.LastModified,
	}, nil
}

func (s *minioStore) Download(ctx context.Context, key string, w io.Writer) error {
	obj, _, err := s.Get(ctx, key)
	if err != nil {
		return err
	}
	defer obj.Close()
	_, err = io.Copy(w, obj)
	return err
}

func (s *minioStore) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *minioStore) DeletePrefix(ctx context.Context, prefix string) error {
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for res := range s.client.RemoveObjects(ctx, s.bucket, objects, minio.RemoveObjectsOptions{}) {
		if res.Err != nil {
			return fmt.Errorf("remove %s: %w", res.ObjectName, res.Err)
		}
	}
	return nil
}

func mapErr(err error) error {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return ErrNotFound
	}
	return err
}
