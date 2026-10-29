package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Catalogue des messages renvoyés au client, sur le modèle de gettext : le
// texte source sert de clé et chaque entrée porte sa version française et sa
// version anglaise. Une entrée est retrouvée par l'une OU l'autre version, si
// bien qu'un message écrit en anglais dans le code s'affiche aussi en français.
//
// Les messages à variables sont des formats printf (« %w: unknown role %q »).
// Ils sont reconnus par expression régulière et les valeurs sont reportées
// dans la traduction, dans le même ordre ou selon %[n]s.
//
// Fichiers : catalog/<module>.json, un tableau [{"fr": …, "en": …}].
// Le test TestEveryMessageIsTranslated échoue si un message du code manque.

//go:embed catalog/*.json
var catalogFS embed.FS

// Entry : un message dans les deux langues.
type Entry struct {
	FR string `json:"fr"`
	EN string `json:"en"`
}

func (e Entry) In(l Locale) string {
	if l == EN {
		return e.EN
	}
	return e.FR
}

type template struct {
	re      *regexp.Regexp
	literal int // longueur du texte fixe : les gabarits les plus précis passent d'abord
	entry   Entry
	side    Locale // langue du format qui a produit l'expression
}

type catalog struct {
	exact     map[string]Entry
	templates []template
}

var (
	loadOnce sync.Once
	loaded   *catalog
	loadErr  error
)

func get() *catalog {
	loadOnce.Do(func() { loaded, loadErr = load() })
	if loadErr != nil {
		panic("i18n: catalogue illisible : " + loadErr.Error())
	}
	return loaded
}

// Entries rend toutes les entrées (tests et outillage).
func Entries() []Entry {
	c := get()
	seen := map[Entry]bool{}
	var out []Entry
	for _, e := range c.exact {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, t := range c.templates {
		if !seen[t.entry] {
			seen[t.entry] = true
			out = append(out, t.entry)
		}
	}
	return out
}

func load() (*catalog, error) {
	c := &catalog{exact: map[string]Entry{}}
	files, err := catalogFS.ReadDir("catalog")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := catalogFS.ReadFile(path.Join("catalog", f.Name()))
		if err != nil {
			return nil, err
		}
		var entries []Entry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name(), err)
		}
		for _, e := range entries {
			if e.FR == "" || e.EN == "" {
				return nil, fmt.Errorf("%s: entrée incomplète %+v", f.Name(), e)
			}
			if hasVerb(e.FR) || hasVerb(e.EN) {
				for _, side := range []Locale{FR, EN} {
					src := e.In(side)
					re, lit, err := compile(src)
					if err != nil {
						return nil, fmt.Errorf("%s: %q: %w", f.Name(), src, err)
					}
					c.templates = append(c.templates, template{re: re, literal: lit, entry: e, side: side})
				}
				continue
			}
			c.put(normalize(e.FR), e, EN)
			c.put(normalize(e.EN), e, FR)
		}
	}
	sort.SliceStable(c.templates, func(i, j int) bool { return c.templates[i].literal > c.templates[j].literal })
	return c, nil
}

// put range e sous key. Un même texte peut figurer dans plusieurs modules avec
// des traductions voisines (« Session invalide » / « session invalide ») : on
// garde celle qui s'affiche le mieux dans la langue cible (accents, majuscule).
func (c *catalog) put(key string, e Entry, target Locale) {
	if prev, ok := c.exact[key]; ok && quality(prev.In(target)) >= quality(e.In(target)) {
		return
	}
	c.exact[key] = e
}

func quality(s string) int {
	q := 0
	for _, r := range s {
		if strings.ContainsRune("éèêàçùâîôûœÉÈÀÇ’«»", r) {
			q++
		}
	}
	if r := []rune(strings.TrimSpace(s)); len(r) > 0 && unicode.IsUpper(r[0]) {
		q += 2
	}
	return q
}

// verb : un verbe printf, indice explicite compris (%[2]s), largeur, précision.
var verb = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z]`)

func hasVerb(s string) bool { return verb.MatchString(strings.ReplaceAll(s, "%%", "")) }

func compile(format string) (*regexp.Regexp, int, error) {
	var b strings.Builder
	b.WriteString(`^`)
	literal := 0
	rest := strings.ReplaceAll(format, "%%", "\x00")
	for {
		loc := verb.FindStringIndex(rest)
		if loc == nil {
			part := strings.ReplaceAll(rest, "\x00", "%")
			b.WriteString(regexp.QuoteMeta(part))
			literal += len(part)
			break
		}
		part := strings.ReplaceAll(rest[:loc[0]], "\x00", "%")
		b.WriteString(regexp.QuoteMeta(part))
		literal += len(part)
		b.WriteString(`(.+?)`)
		rest = rest[loc[1]:]
	}
	b.WriteString(`$`)
	re, err := regexp.Compile(`(?s)` + b.String())
	return re, literal, err
}

// fill reporte les valeurs capturées dans le format cible. Les verbes sont
// ramenés à %s (les valeurs sont déjà du texte), indices explicites conservés.
func fill(target string, values []string) string {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	next := 0
	out := verb.ReplaceAllStringFunc(strings.ReplaceAll(target, "%%", "\x00"), func(v string) string {
		if m := regexp.MustCompile(`^%\[(\d+)\]`).FindStringSubmatch(v); m != nil {
			return "%[" + m[1] + "]s"
		}
		next++
		return fmt.Sprintf("%%[%d]s", next)
	})
	out = strings.ReplaceAll(out, "\x00", "%%")
	return fmt.Sprintf(out, args...)
}

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// Known indique si un texte du code (littéral ou format printf) a son entrée
// dans le catalogue. Sert au test de couverture.
func Known(text string) bool {
	c := get()
	if _, ok := c.exact[normalize(text)]; ok {
		return true
	}
	for _, t := range c.templates {
		if t.entry.FR == text || t.entry.EN == text {
			return true
		}
	}
	return false
}

// Translate rend msg dans la langue l, ou msg inchangé s'il est inconnu du
// catalogue (code machine, texte utilisateur, détail technique).
func Translate(l Locale, msg string) string {
	if strings.TrimSpace(msg) == "" {
		return msg
	}
	c := get()
	if e, ok := c.exact[normalize(msg)]; ok {
		return e.In(l)
	}
	// Erreurs du validator avant les gabarits : le gabarit générique
	// « %w: %s » les attraperait sinon (elles contiennent « : »).
	if out, ok := translateValidation(l, msg); ok {
		return out
	}
	for _, t := range c.templates {
		m := t.re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		if t.side == l {
			return msg // déjà dans la bonne langue
		}
		values := m[1:]
		for i, v := range values {
			values[i] = Translate(l, v) // une valeur peut être elle-même un message connu
		}
		return fill(t.entry.In(l), values)
	}
	return msg
}

// ── Erreurs de validation (validator, via tonic) ────────────────────────────
//
// « Key: 'CreateInput.Email' Error:Field validation for 'Email' failed on the
// 'email' tag », une ligne par champ. Message technique, en anglais, qu'aucun
// utilisateur ne comprend : on le reformule dans les deux langues (le hook
// d'erreurs du routeur renvoie ces lignes sans le préfixe de tonic).

var validationLine = regexp.MustCompile(`Key: '[^']*' Error:Field validation for '([^']+)' failed on the '([^']+)' tag`)

var validationTags = map[string][2]string{
	"required": {"champ obligatoire", "required field"},
	"email":    {"adresse e-mail invalide", "invalid email address"},
	"uuid":     {"identifiant invalide", "invalid identifier"},
	"uuid4":    {"identifiant invalide", "invalid identifier"},
	"min":      {"valeur trop courte ou trop petite", "value too short or too small"},
	"max":      {"valeur trop longue ou trop grande", "value too long or too large"},
	"gte":      {"valeur trop petite", "value too small"},
	"gt":       {"valeur trop petite", "value too small"},
	"lte":      {"valeur trop grande", "value too large"},
	"lt":       {"valeur trop grande", "value too large"},
	"len":      {"longueur incorrecte", "wrong length"},
	"oneof":    {"valeur non autorisée", "value not allowed"},
	"url":      {"adresse web invalide", "invalid web address"},
	"numeric":  {"nombre attendu", "number expected"},
	"datetime": {"date invalide", "invalid date"},
}

func translateValidation(l Locale, msg string) (string, bool) {
	lines := validationLine.FindAllStringSubmatch(msg, -1)
	if len(lines) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(lines))
	for _, m := range lines {
		field, tag := m[1], m[2]
		label, ok := validationTags[tag]
		if !ok {
			label = [2]string{"valeur invalide", "invalid value"}
		}
		if l == EN {
			parts = append(parts, field+": "+label[1])
		} else {
			parts = append(parts, field+" : "+label[0])
		}
	}
	return strings.Join(parts, "\n"), true
}
