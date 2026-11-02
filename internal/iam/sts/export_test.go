package sts

import "time"

// Accès réservés aux tests (horloge, cache d'état).

func (s *Service) SetNow(f func() time.Time) { s.now = f }

func (s *Service) ForgetStatus() {
	s.statusMu.Lock()
	s.status = map[string]statusEntry{}
	s.statusMu.Unlock()
}

func (k *KeyRing) SetNow(f func() time.Time) { k.now = f }
