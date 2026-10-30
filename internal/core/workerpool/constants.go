package workerpool

import "time"

// Valeurs par défaut d'un pool (Config à zéro).
const (
	defaultWorkers     = 8
	defaultQueueSize   = 256
	defaultTaskTimeout = 10 * time.Second
)
