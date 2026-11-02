package ui

import (
	"fmt"
	"sync"
	"time"
)

// Steps : progression d'une opération longue. Dans un terminal, l'étape en
// cours tourne (avec sa durée) et chaque étape franchie reçoit une coche ;
// ailleurs, une ligne par étape.

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerTick = 90 * time.Millisecond

type Steps struct {
	p *Printer

	mu      sync.Mutex
	current string
	started time.Time
	stop    chan struct{}
	done    chan struct{}
}

func (p *Printer) Steps() *Steps { return &Steps{p: p} }

// Start : nouvelle étape ; la précédente est marquée franchie.
func (s *Steps) Start(label string) {
	s.mu.Lock()
	if s.current == label {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.finish(true)
	s.mu.Lock()
	s.current, s.started = label, time.Now()
	s.mu.Unlock()
	if !s.p.animate {
		fmt.Fprintln(s.p.w, "  "+s.p.Dim("·")+" "+label)
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.spin(s.stop, s.done)
}

func (s *Steps) spin(stop, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(spinnerTick)
	defer t.Stop()
	for i := 0; ; i++ {
		s.mu.Lock()
		label, elapsed := s.current, time.Since(s.started).Round(time.Second)
		s.mu.Unlock()
		fmt.Fprintf(s.p.w, "\r\x1b[2K  %s %s  %s", s.p.Brand(spinnerFrames[i%len(spinnerFrames)]), label, s.p.Dim(elapsed.String()))
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// finish : arrête l'animation et fige l'étape courante (franchie ou non).
func (s *Steps) finish(ok bool) {
	s.mu.Lock()
	label, started := s.current, s.started
	s.current = ""
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if label == "" {
		return
	}
	if stop != nil {
		close(stop)
		<-done
		fmt.Fprint(s.p.w, "\r\x1b[2K")
	}
	if !s.p.animate {
		return
	}
	sym := s.p.Green(SymOK)
	if !ok {
		sym = s.p.Red(SymFail)
	}
	fmt.Fprintf(s.p.w, "  %s %s  %s\n", sym, label, s.p.Dim(time.Since(started).Round(time.Second).String()))
}

// Done : dernière étape franchie.
func (s *Steps) Done() { s.finish(true) }

// Failed : l'étape en cours a échoué.
func (s *Steps) Failed() { s.finish(false) }
