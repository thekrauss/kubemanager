#!/usr/bin/env bash
# À lancer sur le serveur 2, après `kubectl apply -f rbac.yaml`. Écrit sur la
# sortie standard le kubeconfig du compte betorun-platform, à copier sur le
# serveur 1 sans passer par un fichier partagé :
#   ssh serveur2 'sudo bash make-kubeconfig.sh <IP-serveur-2>' > /opt/betorun/secrets/kubeconfig
set -euo pipefail
server_ip="${1:?usage : make-kubeconfig.sh <IP du serveur 2 joignable par le serveur 1>}"
secret=betorun-platform-token
ns=betorun-system
token="$(kubectl -n "$ns" get secret "$secret" -o jsonpath='{.data.token}' | base64 -d)"
ca="$(kubectl -n "$ns" get secret "$secret" -o jsonpath='{.data.ca\.crt}')"
[ -n "$token" ] && [ -n "$ca" ] || { echo "jeton absent : rbac.yaml appliqué ?" >&2; exit 1; }
cat <<KUBECONFIG
apiVersion: v1
kind: Config
clusters:
  - name: betorun
    cluster:
      server: https://${server_ip}:6443
      certificate-authority-data: ${ca}
users:
  - name: betorun-platform
    user:
      token: ${token}
contexts:
  - name: betorun
    context:
      cluster: betorun
      user: betorun-platform
current-context: betorun
KUBECONFIG
