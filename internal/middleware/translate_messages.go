package middleware

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/core/i18n"
)

// TranslateMessages rend dans la langue de la requête les textes que l'API
// renvoie au client : champs "message", "error", "detail" des réponses JSON
// d'erreur (statut ≥ 400), et champ "message" de premier niveau des petites
// réponses de succès (« Relance envoyée. »).
//
// Pourquoi ici et pas dans chaque handler : les messages naissent à plus de
// de nombreux endroits (betoerrors.New, gin.H{"error": …} écrits directement, refus
// des middlewares). Les traduire au point de sortie couvre tout d'un coup, et
// le test TestEveryMessageIsTranslated garantit que chacun a sa traduction.
//
// Seules les réponses JSON sont lues, et au plus maxTranslatedBody octets :
// flux (SSE), fichiers et grosses listes passent sans être mis en mémoire.
// Un texte absent du catalogue (code machine, saisie d'un utilisateur) n'est
// jamais modifié.
func TranslateMessages() gin.HandlerFunc {
	return func(c *gin.Context) {
		w := &translatingWriter{ResponseWriter: c.Writer, locale: i18n.From(c.Request.Context())}
		c.Writer = w
		c.Next()
		w.finish()
	}
}

const maxTranslatedBody = 64 << 10

var errorKeys = []string{"message", "error", "detail"}

type translatingWriter struct {
	gin.ResponseWriter
	locale    i18n.Locale
	decided   bool
	buffering bool
	buf       bytes.Buffer
}

func (w *translatingWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	ct := w.Header().Get(httpheader.ContentType)
	w.buffering = strings.Contains(ct, jsonContentMarker) && w.Header().Get(httpheader.ContentLength) == ""
}

func (w *translatingWriter) Write(b []byte) (int, error) {
	w.decide()
	if !w.buffering {
		return w.ResponseWriter.Write(b)
	}
	if w.buf.Len()+len(b) > maxTranslatedBody {
		// Trop gros pour être un message : on rend tel quel, sans traduire.
		w.buffering = false
		if w.buf.Len() > 0 {
			if _, err := w.ResponseWriter.Write(w.buf.Bytes()); err != nil {
				return 0, err
			}
			w.buf.Reset()
		}
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

func (w *translatingWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

// Written / Size : vu des handlers, une réponse mise en mémoire est écrite.
func (w *translatingWriter) Written() bool { return w.buf.Len() > 0 || w.ResponseWriter.Written() }
func (w *translatingWriter) Size() int {
	if w.buf.Len() > 0 {
		return w.buf.Len()
	}
	return w.ResponseWriter.Size()
}

// Flush : une réponse qu'on vide au fil de l'eau n'est pas un message.
func (w *translatingWriter) Flush() {
	if w.buffering && w.buf.Len() > 0 {
		w.buffering = false
		_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		w.buf.Reset()
	}
	w.ResponseWriter.Flush()
}

func (w *translatingWriter) finish() {
	if !w.buffering || w.buf.Len() == 0 {
		return
	}
	body := w.buf.Bytes()
	w.buf = bytes.Buffer{}
	w.buffering = false
	_, _ = w.ResponseWriter.Write(translateBody(body, w.locale, w.Status() >= 400))
}

// translateBody traduit les champs de message d'un objet JSON. Toute autre
// forme (tableau, JSON invalide) est rendue telle quelle.
func translateBody(body []byte, l i18n.Locale, isError bool) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return body
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return body
	}
	keys := errorKeys
	if !isError {
		keys = []string{messageField}
	}
	changed := false
	for _, k := range keys {
		raw, ok := obj[k]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		t := i18n.Translate(l, s)
		if t == s {
			continue
		}
		enc, err := json.Marshal(t)
		if err != nil {
			continue
		}
		obj[k] = enc
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}
