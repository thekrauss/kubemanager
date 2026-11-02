package edge

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
)

const notFoundPage = `<!doctype html><html lang="fr"><head><meta charset="utf-8"><title>404</title></head>` +
	`<body style="font-family:system-ui,sans-serif;margin:4rem auto;max-width:32rem;color:#221A1D">` +
	`<h1>404</h1><p>Page introuvable. / Page not found.</p></body></html>`

// Server sert les fichiers des sites (port public, derrière Caddy) et expose
// le contrôle "ask" du TLS à la demande (port interne).
type Server struct {
	Resolver Resolver
	Store    objectstore.Store
	Logger   *slog.Logger
}

// Handler : trafic public des sites.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveSite)
}

// InternalHandler : /check (ask Caddy on_demand_tls) et /healthz. Ne jamais
// l'exposer publiquement : il révèle quels domaines sont servis.
func (s *Server) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(pathHealthz, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(pathCheck, s.checkDomain)
	return mux
}

// checkDomain : Caddy demande un certificat pour ?domain=X seulement si on
// répond 200. Sans ça, n'importe quel domaine pointé vers l'edge ferait
// émettre des certificats (abus + quotas Let's Encrypt).
func (s *Server) checkDomain(w http.ResponseWriter, r *http.Request) {
	host := normalizeHost(r.URL.Query().Get(checkDomainParam))
	if host == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}
	site, err := s.Resolver.Resolve(r.Context(), host)
	if err != nil {
		s.Logger.Error("resolve host for ask", "host", host, "error", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if site == nil {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) serveSite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set(httpheader.Allow, allowedMethods)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host := normalizeHost(r.Host)
	site, err := s.Resolver.Resolve(r.Context(), host)
	if err != nil {
		s.Logger.Error("resolve host", "host", host, "error", err)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if site == nil {
		writeNotFound(w)
		return
	}

	// path.Clean sur un chemin absolu ne remonte jamais au-dessus de "/".
	clean := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(clean, "/")
	dirRequest := strings.HasSuffix(r.URL.Path, "/") || rel == ""

	if dirRequest {
		if s.tryServe(w, r, site, path.Join(rel, indexFile), http.StatusOK) {
			return
		}
	} else {
		if s.tryServe(w, r, site, rel, http.StatusOK) {
			return
		}
		// /docs → /docs/ si docs/index.html existe : les liens relatifs de la
		// page se résolvent alors dans le bon dossier.
		if s.exists(r, site, path.Join(rel, indexFile)) {
			target := clean + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		// URLs propres : /about → about.html.
		if path.Ext(rel) == "" && s.tryServe(w, r, site, rel+htmlExt, http.StatusOK) {
			return
		}
	}

	if site.SPAFallback && s.tryServe(w, r, site, indexFile, http.StatusOK) {
		return
	}
	if s.tryServe(w, r, site, notFoundFile, http.StatusNotFound) {
		return
	}
	writeNotFound(w)
}

func (s *Server) exists(r *http.Request, site *Site, rel string) bool {
	obj, _, err := s.Store.Get(r.Context(), site.Prefix()+rel)
	if err != nil {
		return false
	}
	_ = obj.Close()
	return true
}

// tryServe sert l'objet s'il existe. status != 200 (page 404 du site) :
// réponse complète sans gestion conditionnelle/Range.
func (s *Server) tryServe(w http.ResponseWriter, r *http.Request, site *Site, rel string, status int) bool {
	obj, info, err := s.Store.Get(r.Context(), site.Prefix()+rel)
	if errors.Is(err, objectstore.ErrNotFound) {
		return false
	}
	if err != nil {
		s.Logger.Error("get object", "site_id", site.ID, "path", rel, "error", err)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return true
	}
	defer obj.Close()

	h := w.Header()
	if info.ContentType != "" {
		h.Set(httpheader.ContentType, info.ContentType)
	}
	h.Set(httpheader.XContentTypeOpts, httpheader.NoSniff)
	if strings.HasSuffix(rel, htmlExt) {
		// HTML toujours revalidé : un nouveau déploiement est visible tout de suite.
		h.Set(httpheader.CacheControl, cacheControlHTML)
	} else {
		h.Set(httpheader.CacheControl, cacheControlAsset)
	}

	if status != http.StatusOK {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, obj)
		}
		return true
	}
	if info.ETag != "" {
		h.Set(httpheader.ETag, `"`+strings.Trim(info.ETag, `"`)+`"`)
	}
	// ServeContent : Range, If-None-Match, If-Modified-Since, HEAD.
	http.ServeContent(w, r, "", info.LastModified, obj)
	return true
}

func writeNotFound(w http.ResponseWriter) {
	w.Header().Set(httpheader.ContentType, httpheader.MIMETextHTMLUTF8)
	w.Header().Set(httpheader.CacheControl, httpheader.NoStore)
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(notFoundPage))
}

// normalizeHost : minuscules, sans port ni point final.
func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}
