package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

// LocaleContextKey : clé gin sous laquelle la langue de la requête est rangée
// (en plus du context.Context, lu par les usecases via i18n.From).
const LocaleContextKey = i18n.GinKey

// Locale résout la langue de la requête une seule fois, au plus tôt (avant
// l'authentification, pour que ses refus puissent être traduits), et la
// range dans le contexte. Règle de priorité : cf. package core/i18n.
//
// La réponse annonce la langue choisie (Content-Language) et précise qu'elle
// dépend de l'en-tête de langue (Vary), pour qu'aucun cache ne serve une
// réponse anglaise à un écran français.
func Locale() gin.HandlerFunc {
	return func(c *gin.Context) {
		l := i18n.FromRequest(c.Request)
		c.Request = c.Request.WithContext(i18n.With(c.Request.Context(), l))
		c.Set(LocaleContextKey, l)
		c.Header("Content-Language", string(l))
		c.Writer.Header().Add("Vary", "Accept-Language")
		c.Next()
	}
}
