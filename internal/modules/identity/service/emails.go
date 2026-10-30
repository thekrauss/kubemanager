package services

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"

	"github.com/thekrauss/kubemanager/internal/core/i18n"
	"github.com/thekrauss/kubemanager/internal/infrastructure/mail"
)

// Emails transactionnels de betorun, en français et en anglais (langue de
// la requête). Les liens mènent aux pages de la console (Next.js).

// Pages de la console qui reçoivent les liens.
const (
	pageVerifyEmail   = "/verify-email"
	pageResetPassword = "/reset-password"
	pageSignIn        = "/sign-in"
	pageForgot        = "/forgot-password"
)

type emailKind int

const (
	emailVerify emailKind = iota
	emailReset
	emailAccountExists
	emailPasswordChanged
)

// emailCopy : textes d'un email dans une langue.
type emailCopy struct {
	Subject, Title, Intro, Button, Outro string
}

var emailCopies = map[emailKind]map[i18n.Locale]emailCopy{
	emailVerify: {
		i18n.FR: {Subject: "Confirmez votre adresse email", Title: "Bienvenue sur betorun",
			Intro:  "Bonjour {{.Name}}, confirmez votre adresse email pour activer votre compte.",
			Button: "Confirmer mon adresse", Outro: "Ce lien est valable 24 heures. Si vous n'avez pas créé de compte, ignorez cet email."},
		i18n.EN: {Subject: "Confirm your email address", Title: "Welcome to betorun",
			Intro:  "Hello {{.Name}}, confirm your email address to activate your account.",
			Button: "Confirm my address", Outro: "This link is valid for 24 hours. If you did not create an account, ignore this email."},
	},
	emailReset: {
		i18n.FR: {Subject: "Réinitialisez votre mot de passe", Title: "Mot de passe oublié",
			Intro:  "Bonjour {{.Name}}, choisissez un nouveau mot de passe pour votre compte betorun.",
			Button: "Choisir un nouveau mot de passe", Outro: "Ce lien est valable 1 heure et ne sert qu'une fois. Si vous n'êtes pas à l'origine de cette demande, ignorez cet email : votre mot de passe ne change pas."},
		i18n.EN: {Subject: "Reset your password", Title: "Forgot your password?",
			Intro:  "Hello {{.Name}}, choose a new password for your betorun account.",
			Button: "Choose a new password", Outro: "This link is valid for 1 hour and works only once. If you did not request this, ignore this email: your password stays the same."},
	},
	emailAccountExists: {
		i18n.FR: {Subject: "Vous avez déjà un compte betorun", Title: "Vous avez déjà un compte",
			Intro:  "Bonjour, quelqu'un (vous, sans doute) a tenté de créer un compte betorun avec cette adresse, qui en a déjà un.",
			Button: "Me connecter", Outro: "Mot de passe oublié ? Réinitialisez-le depuis la page de connexion. Si ce n'était pas vous, ignorez cet email."},
		i18n.EN: {Subject: "You already have a betorun account", Title: "You already have an account",
			Intro:  "Hello, someone (probably you) tried to create a betorun account with this address, which already has one.",
			Button: "Sign in", Outro: "Forgot your password? Reset it from the sign-in page. If this wasn't you, ignore this email."},
	},
	emailPasswordChanged: {
		i18n.FR: {Subject: "Votre mot de passe a été modifié", Title: "Mot de passe modifié",
			Intro:  "Bonjour {{.Name}}, le mot de passe de votre compte betorun vient d'être modifié. Vos autres sessions ont été fermées.",
			Button: "Me connecter", Outro: "Si vous n'êtes pas à l'origine de ce changement, réinitialisez votre mot de passe immédiatement."},
		i18n.EN: {Subject: "Your password was changed", Title: "Password changed",
			Intro:  "Hello {{.Name}}, the password of your betorun account was just changed. Your other sessions were signed out.",
			Button: "Sign in", Outro: "If you did not make this change, reset your password immediately."},
	},
}

var textLayout = texttemplate.Must(texttemplate.New("text").Parse(`{{.Title}}

{{.Intro}}

{{.Button}} : {{.Link}}

{{.Outro}}

— betorun
`))

var htmlLayout = htmltemplate.Must(htmltemplate.New("html").Parse(`<!doctype html>
<html lang="{{.Lang}}"><body style="margin:0;padding:24px;background:#f6f4f2;font-family:'IBM Plex Sans',Arial,sans-serif;color:#1d1a19">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:8px;padding:32px">
<tr><td style="font-size:20px;font-weight:600;color:#7A1F3D;padding-bottom:16px">betorun</td></tr>
<tr><td style="font-size:18px;font-weight:600;padding-bottom:12px">{{.Title}}</td></tr>
<tr><td style="font-size:15px;line-height:1.5;padding-bottom:24px">{{.Intro}}</td></tr>
<tr><td style="padding-bottom:24px"><a href="{{.Link}}" style="display:inline-block;background:#7A1F3D;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:6px;font-weight:600">{{.Button}}</a></td></tr>
<tr><td style="font-size:13px;line-height:1.5;color:#6b6461">{{.Outro}}</td></tr>
</table></td></tr></table></body></html>`))

// renderEmail : email prêt à envoyer, dans la langue de ctx.
func renderEmail(ctx context.Context, kind emailKind, to, name, link string) (mail.Message, error) {
	lang := i18n.From(ctx)
	if lang != i18n.EN {
		lang = i18n.FR
	}
	copy := emailCopies[kind][lang]
	intro, err := fill(copy.Intro, map[string]string{"Name": firstNonBlank(name, strings.Split(to, "@")[0])})
	if err != nil {
		return mail.Message{}, err
	}
	data := map[string]string{"Lang": string(lang), "Title": copy.Title, "Intro": intro, "Button": copy.Button,
		"Outro": copy.Outro, "Link": link}
	var text, html bytes.Buffer
	if err := textLayout.Execute(&text, data); err != nil {
		return mail.Message{}, err
	}
	if err := htmlLayout.Execute(&html, data); err != nil {
		return mail.Message{}, err
	}
	return mail.Message{To: to, Subject: copy.Subject, Text: text.String(), HTML: html.String()}, nil
}

func fill(tpl string, data map[string]string) (string, error) {
	t, err := texttemplate.New("intro").Parse(tpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// consoleLink : lien vers une page de la console, avec un jeton éventuel.
func (s *service) consoleLink(page, token string) string {
	base := strings.TrimRight(s.frontendBaseURL, "/")
	if token == "" {
		return base + page
	}
	return fmt.Sprintf("%s%s?token=%s", base, page, url.QueryEscape(token))
}

func firstNonBlank(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
