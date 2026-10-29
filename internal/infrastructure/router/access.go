package router

import (
	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

type accessKind int

const (
	accessPublic accessKind = iota + 1
	accessAuthenticated
	accessPermission
	accessAPIKeyKind
)

// Access : qui peut appeler une route. Argument obligatoire de AddRoute :
// une route ne peut pas être déclarée sans contrôle d'accès explicite, et une
// valeur vide fait échouer le démarrage (registerGroup).
type Access struct {
	kind       accessKind
	permission iam.Permission
	guard      gin.HandlerFunc
	summary    string
}

// Public : sans authentification (login, callbacks OAuth…). Les routes
// publiques sont transmises à l'AuthMiddleware, plus de liste à tenir à jour.
func Public() Access {
	return Access{kind: accessPublic, summary: "publique"}
}

// Authenticated : tout principal authentifié ; le handler ne touche qu'aux
// données de l'appelant (son profil, ses sessions, son organisation personnelle).
func Authenticated() Access {
	return Access{kind: accessAuthenticated, summary: "tout utilisateur authentifié (données de l'appelant uniquement)"}
}

// guards : fabrique les accès qui ont besoin du middleware de sécurité.
type guards struct {
	mw *security.MiddlewareManager
}

func newGuards(app *App) guards {
	return guards{mw: app.Security.Middleware}
}

// Perm : permission IAM sur la ressource donnée par resolve.
func (g guards) Perm(p iam.Permission, resolve security.ResourceResolver) Access {
	return Access{kind: accessPermission, permission: p, guard: g.mw.Require(p, resolve),
		summary: "permission IAM `" + p.String() + "`"}
}

// PermFor : la permission dépend de la ressource (ex. getIamPolicy d'une
// organisation ou d'un projet).
func (g guards) PermFor(permFor func(iam.ResourceName) (iam.Permission, error), resolve security.ResourceResolver) Access {
	return Access{kind: accessPermission, guard: g.mw.RequireFor(permFor, resolve),
		summary: "permission IAM `*.getIamPolicy` / `*.setIamPolicy` selon le type de ressource"}
}

// APIKeyKind : clé API d'un type donné (ex. "cluster" pour le watcher).
func (g guards) APIKeyKind(kinds ...string) Access {
	return Access{kind: accessAPIKeyKind, guard: g.mw.RequireAPIKeyKind(kinds...),
		summary: "clé API de type " + joinKinds(kinds)}
}

func joinKinds(kinds []string) string {
	out := ""
	for i, k := range kinds {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
