package realtime

import "time"

// API HTTP de Centrifugo.
const (
	// publishPath : endpoint de publication (relatif à api_url).
	publishPath = "/publish"
	// WebSocketPath : endpoint de connexion des navigateurs (relatif à l'URL
	// publique de Centrifugo).
	WebSocketPath = "/connection/websocket"

	publishTimeout = 5 * time.Second
	// errorBodyLimit : octets de la réponse d'erreur repris dans l'erreur.
	errorBodyLimit = 2048
)

// Canaux : project:<projet>:events, project:<projet>:workload:<w>[:deploy].
const (
	// ChannelSeparator : séparateur des segments d'un canal.
	ChannelSeparator = ":"
	// ChannelProjectNamespace : premier segment de tout canal de projet.
	ChannelProjectNamespace = "project"

	projectChannelFormat    = "project:%s:events"
	workloadChannelFormat   = "project:%s:workload:%s"
	deploymentChannelFormat = "project:%s:workload:%s:deploy"
)

// En-tête des jetons de connexion signés (HMAC).
const (
	tokenAlg = "HS256"
	tokenTyp = "JWT"
)
