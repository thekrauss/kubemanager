// Package i18n porte la langue de la requête à travers kubemanager (repris
// de scapi, plateforme SYK).
//
// Une seule règle pour toute l'API : la langue choisie dans la console
// betorun (cookie `btr_locale`, posé par le sélecteur de langue) prime ; à
// défaut, l'en-tête Accept-Language (envoyé explicitement par kweb et la
// CLI) ; à défaut, le français.
//
// Le cookie passe avant l'en-tête parce que le navigateur envoie de lui-même
// sa propre langue dans Accept-Language : un appel qui échapperait au client
// HTTP du front recevrait sinon la langue du navigateur au lieu de celle que
// l'écran affiche.
package i18n

import (
	"context"
	"net/http"
	"strings"

	"golang.org/x/text/language"
)

// Locale : langue prise en charge par betorun.
type Locale string

const (
	FR Locale = "fr"
	EN Locale = "en"

	// Default : langue de repli de toute la plateforme.
	Default = FR

	// CookieName : cookie écrit par le sélecteur de langue de kweb
	// (lib/i18n/config.ts LOCALE_COOKIE_NAME). Les deux valeurs doivent rester égales.
	CookieName = "btr_locale"
)

type ctxKey struct{}

// GinKey : clé chaîne sous laquelle le middleware range aussi la langue
// (c.Set). Un *gin.Context passé tel quel à un usecase ne délègue pas à
// c.Request.Context() (ContextWithFallback n'est pas activé) : seule cette
// clé y est visible, y compris depuis un contexte dérivé (WithTimeout…).
const GinKey = "locale"

// lookup lit la langue par la clé typée, puis par la clé chaîne.
func lookup(ctx context.Context) (Locale, bool) {
	if l, ok := ctx.Value(ctxKey{}).(Locale); ok {
		return l, true
	}
	l, ok := ctx.Value(GinKey).(Locale)
	return l, ok
}

// Parse lit une valeur de cookie ou un en-tête Accept-Language et rend une
// langue prise en charge. ok=false si rien d'exploitable.
func Parse(value string) (Locale, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	switch strings.ToLower(value) {
	case "fr":
		return FR, true
	case "en":
		return EN, true
	}
	tags, _, err := language.ParseAcceptLanguage(value)
	if err != nil {
		return "", false
	}
	for _, tag := range tags {
		base, _ := tag.Base()
		switch base.String() {
		case "fr":
			return FR, true
		case "en":
			return EN, true
		}
	}
	return "", false
}

// Normalize : comme Parse, avec repli sur la langue par défaut.
func Normalize(value string) Locale {
	if l, ok := Parse(value); ok {
		return l
	}
	return Default
}

// FromRequest applique la règle de priorité : cookie, puis Accept-Language,
// puis langue par défaut.
func FromRequest(r *http.Request) Locale {
	if r == nil {
		return Default
	}
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	if l, ok := Parse(r.Header.Get("Accept-Language")); ok {
		return l
	}
	return Default
}

// Explicit : la langue que la requête demande vraiment (cookie ou
// Accept-Language), sans repli. ok=false si elle n'en dit rien : on ne
// l'enregistre alors pas comme préférence du compte.
func Explicit(r *http.Request) (Locale, bool) {
	if r == nil {
		return "", false
	}
	if c, err := r.Cookie(CookieName); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l, true
		}
	}
	return Parse(r.Header.Get("Accept-Language"))
}

// With range la langue dans le contexte.
func With(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From rend la langue du contexte, ou la langue par défaut (tâches de fond,
// workers : ils doivent passer explicitement la langue du destinataire).
func From(ctx context.Context) Locale {
	if ctx != nil {
		if l, ok := lookup(ctx); ok && l != "" {
			return l
		}
	}
	return Default
}

// IsSet indique si une langue a été posée explicitement dans le contexte.
func IsSet(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := lookup(ctx)
	return ok
}

// Pick choisit le texte dans la langue du contexte.
func Pick(ctx context.Context, fr, en string) string {
	if From(ctx) == EN {
		return en
	}
	return fr
}
