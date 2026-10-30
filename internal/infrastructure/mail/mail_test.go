package mail

import (
	"bufio"
	"context"
	"io"
	"mime"
	"net"
	"net/textproto"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSMTP : serveur SMTP minimal qui capture le message reçu.
func fakeSMTP(t *testing.T) (port int, received chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	received = make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		_ = tp.PrintfLine("220 fake")
		var data strings.Builder
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_ = tp.PrintfLine("250 fake")
			case strings.HasPrefix(line, "DATA"):
				_ = tp.PrintfLine("354 go")
				r := bufio.NewReader(tp.DotReader())
				b, _ := io.ReadAll(r)
				data.Write(b)
				_ = tp.PrintfLine("250 ok")
				received <- data.String()
			case strings.HasPrefix(line, "QUIT"):
				_ = tp.PrintfLine("221 bye")
				return
			default:
				_ = tp.PrintfLine("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, received
}

func TestSMTP_SendMultipart(t *testing.T) {
	port, received := fakeSMTP(t)
	s, err := NewSMTP(Config{Host: "127.0.0.1", Port: port, FromEmail: "no-reply@betorun.app", FromName: "betorun"})
	require.NoError(t, err)
	require.NoError(t, s.Send(context.Background(), Message{To: "awa@ex.sn", Subject: "Vérifiez votre adresse",
		Text: "Bonjour Awa, cliquez : https://console.betorun.app/verify-email?token=abc", HTML: "<p>Bonjour Awa</p>"}))

	msg := <-received
	assert.Contains(t, msg, `From: "betorun" <no-reply@betorun.app>`)
	assert.Contains(t, msg, "To: <awa@ex.sn>")
	dec := new(mime.WordDecoder)
	subjectLine := msg[strings.Index(msg, "Subject: ")+len("Subject: "):]
	subject, err := dec.DecodeHeader(subjectLine[:strings.Index(subjectLine, "\n")])
	require.NoError(t, err)
	assert.Equal(t, "Vérifiez votre adresse", subject, "objet encodé (accents)")
	assert.Contains(t, msg, "multipart/alternative")
	assert.Contains(t, msg, "text/plain; charset=utf-8")
	assert.Contains(t, msg, "text/html; charset=utf-8")
	assert.NotContains(t, strings.ToLower(msg), "keycloak")
}

func TestNewSMTP_RequiresConfig(t *testing.T) {
	_, err := NewSMTP(Config{Host: "smtp", Port: 0, FromEmail: "x@y"})
	assert.Error(t, err)
}
