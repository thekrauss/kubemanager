package router

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/loopfz/gadgeto/tonic"
)

// bindHook : remplace le hook par défaut de tonic, qui validait les règles
// `binding:"required"` au décodage du corps JSON, AVANT de lire les
// paramètres de chemin et de requête : toute route POST/PUT/PATCH avec un
// paramètre de chemin obligatoire (ex. POST /projects/:id/sites) répondait
// « ProjectID : champ obligatoire ». Ici, les champs `path:` et `query:` sont
// remplis d'abord, puis le corps est décodé et l'ensemble validé.
func bindHook(maxBodyBytes int64) tonic.BindHook {
	return func(c *gin.Context, i interface{}) error {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		if c.Request.ContentLength == 0 || c.Request.Method == http.MethodGet {
			return nil // tonic lit chemin et requête ensuite, comme avant
		}
		if err := fillParams(c, reflect.ValueOf(i)); err != nil {
			return err
		}
		if err := c.ShouldBindWith(i, binding.JSON); err != nil && err != io.EOF {
			return fmt.Errorf("error parsing request body: %s", err.Error())
		}
		return nil
	}
}

// fillParams : champs étiquetés path:"…" ou query:"…" (chaînes, booléens,
// entiers ; structures imbriquées anonymes comprises), depuis l'URL. Un
// paramètre de requête absent laisse le champ à sa valeur zéro.
func fillParams(c *gin.Context, v reflect.Value) error {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f, fv := t.Field(i), v.Field(i)
		if f.Anonymous {
			if err := fillParams(c, fv.Addr()); err != nil {
				return err
			}
			continue
		}
		if !fv.CanSet() {
			continue
		}
		var name, raw string
		if name = tagName(f.Tag.Get(tonic.PathTag)); name != "" {
			raw = c.Param(name)
		} else if name = tagName(f.Tag.Get(tonic.QueryTag)); name != "" {
			raw = c.Query(name)
		} else {
			continue
		}
		if err := setParam(fv, raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func setParam(fv reflect.Value, raw string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		if raw == "" {
			return nil
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if raw == "" {
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetInt(n)
	}
	return nil
}

func tagName(tag string) string { return strings.Split(tag, ",")[0] }
