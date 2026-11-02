package ui

import (
	"fmt"
	"strings"
)

// Table : colonnes alignées sur la largeur affichée (les codes de couleur ne
// comptent pas, contrairement à text/tabwriter), en-têtes atténués.
type Table struct {
	p       *Printer
	headers []string
	rows    [][]string
}

func (p *Printer) Table(headers ...string) *Table { return &Table{p: p, headers: headers} }

func (t *Table) Row(cells ...string) { t.rows = append(t.rows, cells) }

func (t *Table) Flush() error {
	cols := len(t.headers)
	for _, r := range t.rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	widths := make([]int, cols)
	measure := func(r []string) {
		for i, c := range r {
			if w := Width(c); w > widths[i] {
				widths[i] = w
			}
		}
	}
	measure(t.headers)
	for _, r := range t.rows {
		measure(r)
	}
	line := func(r []string, style func(string) string) error {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			if i == cols-1 {
				b.WriteString(style(cell))
				break
			}
			b.WriteString(style(cell) + strings.Repeat(" ", widths[i]-Width(cell)+2))
		}
		_, err := fmt.Fprintln(t.p.w, strings.TrimRight(b.String(), " "))
		return err
	}
	if len(t.headers) > 0 {
		upper := make([]string, len(t.headers))
		for i, h := range t.headers {
			upper[i] = strings.ToUpper(h)
		}
		if err := line(upper, t.p.Dim); err != nil {
			return err
		}
	}
	for _, r := range t.rows {
		if err := line(r, func(s string) string { return s }); err != nil {
			return err
		}
	}
	return nil
}
