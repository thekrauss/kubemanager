package iam

import (
	"sync"
	"time"
)

// Diffusion des changements de bindings, pour que les retraits de droits
// prennent effet sans attendre l'expiration des caches.
//
// Les écritures (AddBinding, RemovePrincipal, DeleteResourcePolicy) notifient
// les hooks enregistrés ; l'application les relaie entre process (Redis).
// Une écriture peut se produire dans une transaction pas encore validée :
// la notification est donc répétée après un court délai, pour couvrir une
// relecture qui aurait eu lieu avant le commit.

var (
	hooksMu     sync.RWMutex
	changeHooks []func(Principal)
)

// renotifyAfter : seconde notification, après le commit probable.
const renotifyAfter = 2 * time.Second

// OnBindingsChanged enregistre un hook appelé avec chaque principal dont les
// bindings ont changé. AllUsers signifie « tous » (invalidation globale).
func OnBindingsChanged(f func(Principal)) {
	hooksMu.Lock()
	changeHooks = append(changeHooks, f)
	hooksMu.Unlock()
}

func notifyChanged(p Principal) {
	fire(p)
	time.AfterFunc(renotifyAfter, func() { fire(p) })
}

func fire(p Principal) {
	hooksMu.RLock()
	hooks := changeHooks
	hooksMu.RUnlock()
	for _, f := range hooks {
		f(p)
	}
}
