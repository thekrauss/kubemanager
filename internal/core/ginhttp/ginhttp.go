// Package ginhttp : aides communes aux handlers gin « bruts » (hors tonic).
package ginhttp

import (
	"io"

	"github.com/gin-gonic/gin"
)

const copyBufferSize = 32 * 1024

// CopyFlushing : recopie r vers le client, sans retenir les données en tampon :
// sans Flush, un flux suivi (logs --follow) reste muet.
func CopyFlushing(c *gin.Context, r io.Reader) {
	buf := make([]byte, copyBufferSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := c.Writer.Write(buf[:n]); werr != nil {
				return
			}
			c.Writer.Flush()
		}
		if err != nil {
			return
		}
	}
}

// RemoveMultipart : supprime les fichiers temporaires d'un formulaire multipart.
// net/http ne nettoie que la requête d'origine : or les middlewares remplacent
// c.Request (WithContext), et le formulaire est analysé sur la copie. À différer
// avant la lecture du formulaire.
func RemoveMultipart(c *gin.Context) {
	if c.Request.MultipartForm != nil {
		_ = c.Request.MultipartForm.RemoveAll()
	}
}
