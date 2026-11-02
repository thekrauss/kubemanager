#!/bin/sh
# Espace de noms de la plateforme ; sans effet s'il existe déjà.
set -eu
ADDRESS="temporal:7233"
if ! temporal operator namespace describe --address "$ADDRESS" --namespace "$TEMPORAL_NAMESPACE" >/dev/null 2>&1; then
  temporal operator namespace create --address "$ADDRESS" --namespace "$TEMPORAL_NAMESPACE" --retention 168h
fi
