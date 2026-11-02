// Package registry : accès au registre d'images de la plateforme.
//
// Le registre délègue son authentification à l'API (Docker token auth) :
// un client présente un identifiant, l'API répond par un jeton signé qui ne
// donne accès qu'aux dépôts du projet concerné. Deux identifiants, dérivés
// d'un secret serveur (HMAC), sans stockage :
//
//	build.<projet>.<expiration>  écriture et lecture, le temps d'un build
//	pull.<projet>                lecture, pour les nœuds qui lancent les pods
//
// Le code d'un Dockerfile s'exécute à côté de l'identifiant de son build :
// il faut le considérer comme lisible. Il ne donne donc rien de plus que ce
// que le projet a déjà, et expire avec le build.
package registry

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Actions sur un dépôt.
const (
	ActionPull = "pull"
	ActionPush = "push"
)

const (
	userBuild = "build"
	userPull  = "pull"

	repositoryScope = "repository"
	tokenLifetime   = 5 * time.Minute

	// PullSecretName : Secret du namespace du projet, utilisé par les nœuds.
	PullSecretName = "betorun-registry"
)

// ErrBadCredentials : identifiant inconnu, expiré ou signature fausse.
var ErrBadCredentials = errors.New("invalid registry credentials")

// Credentials : dérive et vérifie les identifiants du registre.
type Credentials struct {
	Secret []byte
}

func (c Credentials) sign(parts ...string) string {
	mac := hmac.New(sha256.New, c.Secret)
	mac.Write([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(mac.Sum(nil))
}

// ForBuild : identifiant d'un build, valable jusqu'à expires.
func (c Credentials) ForBuild(project uuid.UUID, expires time.Time) (user, password string) {
	exp := strconv.FormatInt(expires.Unix(), 10)
	return userBuild + "." + project.String() + "." + exp, c.sign(userBuild, project.String(), exp)
}

// ForPull : identifiant de lecture d'un projet.
func (c Credentials) ForPull(project uuid.UUID) (user, password string) {
	return userPull + "." + project.String(), c.sign(userPull, project.String())
}

// Verify : projet et actions accordées par cet identifiant.
func (c Credentials) Verify(user, password string, now time.Time) (uuid.UUID, []string, error) {
	parts := strings.Split(user, ".")
	if len(parts) < 2 {
		return uuid.Nil, nil, ErrBadCredentials
	}
	project, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, nil, ErrBadCredentials
	}
	var want string
	var actions []string
	switch {
	case parts[0] == userBuild && len(parts) == 3:
		exp, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || now.Unix() > exp {
			return uuid.Nil, nil, ErrBadCredentials
		}
		want = c.sign(userBuild, parts[1], parts[2])
		actions = []string{ActionPull, ActionPush}
	case parts[0] == userPull && len(parts) == 2:
		want = c.sign(userPull, parts[1])
		actions = []string{ActionPull}
	default:
		return uuid.Nil, nil, ErrBadCredentials
	}
	if !hmac.Equal([]byte(want), []byte(password)) {
		return uuid.Nil, nil, ErrBadCredentials
	}
	return project, actions, nil
}

// SourceSignature : signature de l'URL de téléchargement d'une archive.
func (c Credentials) SourceSignature(build uuid.UUID, expires int64) string {
	return c.sign("source", build.String(), strconv.FormatInt(expires, 10))
}

// VerifySource : URL signée encore valable.
func (c Credentials) VerifySource(build uuid.UUID, expires int64, sig string, now time.Time) bool {
	return now.Unix() <= expires && hmac.Equal([]byte(c.SourceSignature(build, expires)), []byte(sig))
}

// DockerConfigJSON : fichier d'identifiants compris par BuildKit et Kubernetes.
func DockerConfigJSON(host, user, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	return json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{
		"username": user, "password": password, "auth": auth,
	}}})
}

// access : entrée « access » du jeton.
type access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type tokenClaims struct {
	jwt.RegisteredClaims
	Access []access `json:"access"`
}

// Issuer : signe les jetons du registre. Le registre vérifie la chaîne x5c
// contre le certificat qu'il a reçu (rootcertbundle).
type Issuer struct {
	Issuer, Service string
	key             *ecdsa.PrivateKey
	x5c             []string
}

// NewIssuer : clé ECDSA P-256 et son certificat, fichiers PEM.
func NewIssuer(issuer, service, certFile, keyFile string) (*Issuer, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("registry token key: %w", err)
	}
	key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("registry token key: ECDSA P-256 key expected")
	}
	x5c := make([]string, 0, len(pair.Certificate))
	for _, der := range pair.Certificate {
		x5c = append(x5c, base64.StdEncoding.EncodeToString(der))
	}
	return &Issuer{Issuer: issuer, Service: service, key: key, x5c: x5c}, nil
}

// Issue : jeton pour les scopes demandés, réduits à ce que l'identifiant
// autorise. Un scope hors du projet n'est pas une erreur du protocole : il
// n'est simplement pas accordé, et le registre refusera l'accès.
func (i *Issuer) Issue(user string, project uuid.UUID, granted []string, scopes []string, now time.Time) (string, int, error) {
	claims := tokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.Issuer,
			Subject:   user,
			Audience:  jwt.ClaimStrings{i.Service},
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenLifetime)),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        randomID(),
		},
		Access: Grant(project, granted, scopes),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	t.Header["x5c"] = i.x5c
	s, err := t.SignedString(i.key)
	return s, int(tokenLifetime.Seconds()), err
}

// Grant : scopes « repository:<dépôt>:<actions> » accordés au projet.
func Grant(project uuid.UUID, granted, scopes []string) []access {
	out := []access{}
	prefix := project.String() + "/"
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 3 || parts[0] != repositoryScope || !strings.HasPrefix(parts[1], prefix) || strings.Contains(parts[1], "..") {
			continue
		}
		var actions []string
		for _, want := range strings.Split(parts[2], ",") {
			for _, have := range granted {
				if want == have {
					actions = append(actions, want)
				}
			}
		}
		if len(actions) > 0 {
			out = append(out, access{Type: repositoryScope, Name: parts[1], Actions: actions})
		}
	}
	return out
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// IsPlatformImage : image du registre de la plateforme.
func IsPlatformImage(host, image string) bool {
	return host != "" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(image)), strings.ToLower(host)+"/")
}

// OwnedBy : l'image du registre de la plateforme appartient à ce projet.
// Sans ce contrôle, un nœud qui a déjà l'image d'un autre projet en cache
// la lancerait sans demander de droits au registre.
func OwnedBy(host, image string, project uuid.UUID) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(image)), strings.ToLower(host)+"/"+project.String()+"/")
}

// ImageName : <registre>/<projet>/<nom>.
func ImageName(host string, project uuid.UUID, name string) string {
	return host + "/" + project.String() + "/" + name
}
