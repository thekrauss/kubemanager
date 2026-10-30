package api

import (
	"io"
	"mime/multipart"
)

// multipartWriter : envoi en flux (l'archive n'est jamais entièrement en mémoire).
type multipartWriter struct{ w *multipart.Writer }

func newMultipart(w io.Writer) *multipartWriter { return &multipartWriter{w: multipart.NewWriter(w)} }

func (m *multipartWriter) contentType() string { return m.w.FormDataContentType() }

func (m *multipartWriter) writeFile(field, filename string, data io.Reader) error {
	part, err := m.w.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, data); err != nil {
		return err
	}
	return m.w.Close()
}
