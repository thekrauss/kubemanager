// Package extract trouve, dans le code kubemanager, tous les textes qui
// peuvent arriver au client comme message : c'est la liste que le catalogue
// de traductions (core/i18n/catalog/*.json) doit couvrir. Repris de scapi
// (plateforme SYK), adapté aux formes de messages d'ici.
//
// Sont relevés :
//   - betoerrors.New(code, MSG), betoerrors.Wrap(cause, code, MSG),
//     betoerrors.Newf(code, FORMAT, …) et les littéraux
//     &betoerrors.Error{Message: MSG} ;
//   - les valeurs des clés "error", "message", "detail" d'un gin.H{…} ;
//   - les erreurs sentinelles déclarées au niveau du paquet
//     (var ErrX = errors.New(MSG) / fmt.Errorf(FORMAT, …)) : le hook d'erreurs
//     du routeur les renvoie au client ;
//   - fmt.Errorf("%w: …", ErrX, …) : détail ajouté à une sentinelle, renvoyé
//     tel quel pour les sentinelles « détaillées » ;
//   - partout ailleurs, les errors.New / fmt.Errorf rédigés en FRANÇAIS.
//
// Les erreurs techniques (anglais, sans sentinelle) ne sont pas relevées :
// le hook les remplace par un 500 générique, elles n'arrivent jamais au client.
package extract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Message : un texte relevé dans le code.
type Message struct {
	Text     string // texte, ou format printf si Template
	Template bool   // contient des verbes %s/%d/%v/%w…
	Pos      string // fichier:ligne du premier relevé
}

// Result : tout ce qu'a trouvé un parcours.
type Result struct {
	Messages   []Message // dédoublonnés, triés
	Unresolved []string  // positions où le message n'est pas un texte vérifiable
}

var messageKeys = map[string]bool{"error": true, "message": true, "detail": true}

// Dir parcourt root (typiquement kubemanager/internal) hors tests.
func Dir(root string) (Result, error) {
	seen := map[string]Message{}
	var unresolved []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Le moteur de traduction lui-même ne renvoie rien au client.
			if d.Name() == "vendor" || d.Name() == "testdata" || strings.HasSuffix(filepath.ToSlash(path), "core/i18n") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_generated.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		add := func(expr ast.Expr, isFormat bool) {
			text, template, ok := resolve(expr, isFormat)
			pos := fset.Position(expr.Pos())
			where := pos.Filename + ":" + strconv.Itoa(pos.Line)
			if !ok {
				unresolved = append(unresolved, where)
				return
			}
			if strings.TrimSpace(text) == "" || !looksHuman(text) {
				return
			}
			if _, dup := seen[text]; !dup {
				seen[text] = Message{Text: text, Template: template, Pos: where}
			}
		}
		// Sentinelles au niveau du paquet, et constantes de message en français.
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if ok && gen.Tok == token.CONST {
				for _, spec := range gen.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, v := range vs.Values {
							if frenchLiteral(v) {
								add(v, false)
							}
						}
					}
				}
				continue
			}
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					call, ok := v.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 {
						continue
					}
					switch selector(call.Fun) {
					case "errors.New":
						add(call.Args[0], false)
					case "fmt.Errorf":
						add(call.Args[0], true)
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				switch selector(node.Fun) {
				case "betoerrors.New":
					if len(node.Args) == 2 {
						add(node.Args[1], false)
					}
				case "betoerrors.Wrap":
					if len(node.Args) == 3 {
						add(node.Args[2], false)
					}
				case "betoerrors.Newf":
					if len(node.Args) >= 2 {
						add(node.Args[1], true)
					}
				default:
					// Helpers locaux (fonction ou méthode) qui portent un message destiné au client.
					if h, ok := messageHelpers[funcName(node.Fun)]; ok && h.arg < len(node.Args) {
						add(node.Args[h.arg], h.format)
					}
				case "errors.New":
					if len(node.Args) == 1 && frenchLiteral(node.Args[0]) {
						add(node.Args[0], false)
					}
				case "fmt.Errorf":
					if len(node.Args) >= 1 && (frenchLiteral(node.Args[0]) || wrapsWithDetail(node.Args[0])) {
						add(node.Args[0], true)
					}
				}
			case *ast.CompositeLit:
				// &betoerrors.Error{Code: …, Message: "…"}
				if selector(node.Type) == "betoerrors.Error" {
					for _, elt := range node.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Message" && isStringish(kv.Value) {
								add(kv.Value, false)
							}
						}
					}
					return true
				}
				if selector(node.Type) != "gin.H" {
					return true
				}
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.BasicLit)
					if !ok || key.Kind != token.STRING {
						continue
					}
					k, _ := strconv.Unquote(key.Value)
					if messageKeys[k] && isStringish(kv.Value) {
						add(kv.Value, false)
					}
				}
			}
			return true
		})
		return nil
	})
	out := make([]Message, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Text < out[j].Text })
	return Result{Messages: out, Unresolved: unresolved}, err
}

// messageHelpers : fonctions locales dont un argument est un message pour
// l'utilisateur (index de l'argument, et s'il s'agit d'un format printf).
var messageHelpers = map[string]struct {
	arg    int
	format bool
}{
	"notFound":       {1, false}, // sites : notFound(err, "site introuvable")
	"archiveErrorf":  {0, true},  // sites : erreurs d'archive enregistrées sur le déploiement
	"failDeployment": {2, false}, // sites : message d'échec enregistré sur le déploiement
}

// wrapsWithDetail : fmt.Errorf("%w: …") — une sentinelle enrichie d'un détail.
func wrapsWithDetail(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(lit.Value)
	return err == nil && strings.HasPrefix(s, "%w: ")
}

// funcName : nom d'une fonction locale ou d'une méthode appelée (s.failDeployment).
func funcName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

func selector(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name + "." + sel.Sel.Name
}

func isStringish(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.BinaryExpr:
		return v.Op == token.ADD
	}
	return false
}

// resolve rend le texte (ou le format) d'une expression de message.
func resolve(e ast.Expr, isFormat bool) (string, bool, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false, false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false, false
		}
		return s, isFormat && hasVerb(s), true
	case *ast.CallExpr:
		if selector(v.Fun) == "fmt.Sprintf" && len(v.Args) >= 1 {
			if lit, ok := v.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err == nil {
					return s, hasVerb(s), true
				}
			}
		}
		return "", false, false
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false, false
		}
		var b strings.Builder
		var walk func(ast.Expr) bool
		walk = func(x ast.Expr) bool {
			if be, ok := x.(*ast.BinaryExpr); ok && be.Op == token.ADD {
				return walk(be.X) && walk(be.Y)
			}
			if lit, ok := x.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return false
				}
				b.WriteString(strings.ReplaceAll(s, "%", "%%"))
				return true
			}
			b.WriteString("%s")
			return true
		}
		if !walk(v) {
			return "", false, false
		}
		s := b.String()
		return s, hasVerb(s), true
	}
	return "", false, false
}

// IsFrench : accent ou mot outil français. Sert à trier les erreurs métier
// (rédigées en français pour l'utilisateur) des erreurs techniques.
func IsFrench(s string) bool {
	if strings.ContainsAny(s, "éèêàçùâîôûœÉÈÀÇ") {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r > 127)
	}) {
		switch w {
		case "est", "pas", "une", "des", "les", "pour", "doit", "requis", "requise", "invalide",
			"introuvable", "impossible", "aucun", "aucune", "cette", "votre", "vous", "non", "deja", "dans", "avec", "sans":
			return true
		}
	}
	return false
}

func frenchLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(lit.Value)
	return err == nil && IsFrench(s)
}

func hasVerb(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' {
			if s[i+1] == '%' {
				i++
				continue
			}
			return true
		}
	}
	return false
}

// looksHuman écarte les codes machine (« missing_key », « INVALID_INPUT ») :
// seul un texte lisible a besoin d'une traduction.
func looksHuman(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.ContainsAny(t, " ") {
		return false
	}
	letters := 0
	for _, r := range t {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127 {
			letters++
		}
	}
	return letters >= 3
}
