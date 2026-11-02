// Package ui : mise en forme de la sortie de la CLI (couleurs, états,
// tableaux, étapes animées, liens). Rien n'est décoré hors d'un vrai
// terminal : scripts, CI et redirections reçoivent du texte simple
// (convention NO_COLOR ; FORCE_COLOR pour forcer).
package ui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// Variables d'environnement reconnues.
const (
	envNoColor    = "NO_COLOR"
	envForceColor = "FORCE_COLOR"
	envCI         = "CI"
	envTerm       = "TERM"
	envColorTerm  = "COLORTERM"
)

// Codes ANSI.
const (
	reset     = "\x1b[0m"
	bold      = "\x1b[1m"
	dim       = "\x1b[2m"
	underline = "\x1b[4m"
	red       = "\x1b[31m"
	green     = "\x1b[32m"
	yellow    = "\x1b[33m"
	cyan      = "\x1b[36m"
	// brandTrue / brand256 : bordeaux betorun (#7A1F3D), éclairci pour rester
	// lisible sur fond sombre comme sur fond clair.
	brandTrue = "\x1b[38;2;196;72;112m"
	brand256  = "\x1b[38;5;168m"
)

// Symboles.
const (
	SymOK    = "✓"
	SymFail  = "✗"
	SymWarn  = "!"
	SymDot   = "●"
	SymArrow = "→"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b]8;;[^\x1b]*\x1b\\`)

// Width : largeur affichée (sans les codes de mise en forme).
func Width(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

// Printer : sortie mise en forme selon les capacités du terminal.
type Printer struct {
	w         io.Writer
	color     bool
	animate   bool
	truecolor bool
}

// New : capacités déduites de w (terminal ou non) et de l'environnement.
func New(w io.Writer) *Printer {
	p := &Printer{w: w}
	tty := false
	if f, ok := w.(*os.File); ok {
		tty = term.IsTerminal(int(f.Fd()))
	}
	switch {
	case os.Getenv(envNoColor) != "":
		p.color = false
	case os.Getenv(envForceColor) != "":
		p.color = true
	default:
		p.color = tty && os.Getenv(envTerm) != "dumb"
	}
	p.animate = p.color && tty && os.Getenv(envCI) == ""
	ct := os.Getenv(envColorTerm)
	p.truecolor = ct == "truecolor" || ct == "24bit"
	return p
}

// Plain : jamais de mise en forme (tests, sorties machine).
func Plain(w io.Writer) *Printer { return &Printer{w: w} }

func (p *Printer) Writer() io.Writer { return p.w }
func (p *Printer) Color() bool       { return p.color }

func (p *Printer) style(code, s string) string {
	if !p.color || s == "" {
		return s
	}
	return code + s + reset
}

func (p *Printer) Bold(s string) string   { return p.style(bold, s) }
func (p *Printer) Dim(s string) string    { return p.style(dim, s) }
func (p *Printer) Green(s string) string  { return p.style(green, s) }
func (p *Printer) Yellow(s string) string { return p.style(yellow, s) }
func (p *Printer) Red(s string) string    { return p.style(red, s) }
func (p *Printer) Cyan(s string) string   { return p.style(cyan, s) }

// Brand : couleur de la marque betorun.
func (p *Printer) Brand(s string) string {
	if p.truecolor {
		return p.style(brandTrue, s)
	}
	return p.style(brand256, s)
}

// Link : adresse cliquable (OSC 8) et soulignée dans un terminal.
func (p *Printer) Link(url string) string {
	if !p.color {
		return url
	}
	return "\x1b]8;;" + url + "\x1b\\" + underline + cyan + url + reset + "\x1b]8;;\x1b\\"
}

// Println : ligne brute.
func (p *Printer) Println(a ...any) { fmt.Fprintln(p.w, a...) }

// Success / Warn / Fail / Info : messages précédés de leur symbole.
func (p *Printer) Success(msg string) { fmt.Fprintln(p.w, p.Green(SymOK)+" "+msg) }
func (p *Printer) Warn(msg string)    { fmt.Fprintln(p.w, p.Yellow(SymWarn)+" "+msg) }
func (p *Printer) Fail(msg string)    { fmt.Fprintln(p.w, p.Red(SymFail)+" "+msg) }
func (p *Printer) Info(msg string)    { fmt.Fprintln(p.w, p.Brand(SymDot)+" "+msg) }

// Hint : indication secondaire (commande suivante, aide).
func (p *Printer) Hint(msg string) { fmt.Fprintln(p.w, p.Dim(msg)) }

// Title : titre d'une opération (« ● Déploiement de api  image »).
func (p *Printer) Title(msg, detail string) {
	line := p.Brand(SymDot) + " " + p.Bold(msg)
	if detail != "" {
		line += "  " + p.Dim(detail)
	}
	fmt.Fprintln(p.w, line)
}

// Fields : paires libellé / valeur alignées (fiche d'un objet).
func (p *Printer) Fields(pairs ...[2]string) {
	width := 0
	for _, kv := range pairs {
		if w := Width(kv[0]); w > width {
			width = w
		}
	}
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		fmt.Fprintf(p.w, "%s%s  %s\n", p.Dim(kv[0]), strings.Repeat(" ", width-Width(kv[0])), kv[1])
	}
}

// Tone : catégorie visuelle d'un état.
type Tone int

const (
	ToneNeutral Tone = iota
	ToneOK
	ToneProgress
	ToneError
)

// Status : pastille colorée suivie du libellé (« ● en service »).
func (p *Printer) Status(t Tone, label string) string {
	dot := SymDot
	switch t {
	case ToneOK:
		dot = p.Green(dot)
	case ToneProgress:
		dot = p.Yellow(dot)
	case ToneError:
		dot = p.Red(dot)
	default:
		dot = p.Dim(dot)
	}
	return dot + " " + label
}
