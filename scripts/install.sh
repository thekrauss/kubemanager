#!/bin/sh
# Installe la CLI betorun (Linux, macOS).
#
#   curl -fsSL https://raw.githubusercontent.com/thekrauss/kubemanager/main/scripts/install.sh | sh
#
# Variables :
#   BETORUN_VERSION      version (ex. v0.1.0) ; défaut : la dernière publiée
#   BETORUN_INSTALL_DIR  dossier d'installation ; défaut : /usr/local/bin si
#                        accessible en écriture, sinon ~/.local/bin
#   BETORUN_REPO         dépôt GitHub (défaut thekrauss/kubemanager)
#   BETORUN_DOWNLOAD_URL base de téléchargement (miroir) ; défaut : GitHub Releases
#
# La somme SHA-256 du binaire est vérifiée avant installation.
set -eu

REPO="${BETORUN_REPO:-thekrauss/kubemanager}"
VERSION="${BETORUN_VERSION:-}"

say() { printf '%s\n' "$*"; }
fail() { printf 'betorun install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "système non pris en charge : $(uname -s) (Windows : téléchargez betorun_<version>_windows_amd64.exe depuis https://github.com/${REPO}/releases)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "architecture non prise en charge : $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  download() { wget -qO "$2" "$1"; }
else
  fail "curl ou wget est requis"
fi

# Dernière version de la CLI (étiquettes cli-vX.Y.Z).
if [ -z "$VERSION" ]; then
  tag="$(fetch "https://api.github.com/repos/${REPO}/releases?per_page=50" |
    grep -o '"tag_name": *"cli-v[^"]*"' | head -n 1 | sed 's/.*"\(cli-v[^"]*\)"/\1/')" ||
    fail "impossible de trouver la dernière version"
  [ -n "$tag" ] || fail "aucune version publiée de la CLI"
  VERSION="${tag#cli-}"
fi
TAG="cli-${VERSION}"
BASE="${BETORUN_DOWNLOAD_URL:-https://github.com/${REPO}/releases/download/${TAG}}"
BIN="betorun_${VERSION}_${os}_${arch}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Téléchargement de betorun ${VERSION} (${os}/${arch})…"
download "${BASE}/${BIN}" "${tmp}/${BIN}" || fail "téléchargement impossible : ${BASE}/${BIN}"
download "${BASE}/SHA256SUMS" "${tmp}/SHA256SUMS" || fail "téléchargement impossible : ${BASE}/SHA256SUMS"

expected="$(grep " ${BIN}\$" "${tmp}/SHA256SUMS" | cut -d ' ' -f 1)"
[ -n "$expected" ] || fail "${BIN} absent de SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp}/${BIN}" | cut -d ' ' -f 1)"
else
  actual="$(shasum -a 256 "${tmp}/${BIN}" | cut -d ' ' -f 1)"
fi
[ "$expected" = "$actual" ] || fail "somme SHA-256 incorrecte : téléchargement corrompu ou altéré, installation annulée"

dir="${BETORUN_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="${HOME}/.local/bin"; fi
fi
mkdir -p "$dir"
chmod 0755 "${tmp}/${BIN}"
mv "${tmp}/${BIN}" "${dir}/betorun"

say "betorun ${VERSION} installé dans ${dir}/betorun"
case ":${PATH}:" in
  *":${dir}:"*) ;;
  *) say "Ajoutez ${dir} à votre PATH, par exemple : echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.profile" ;;
esac
say "Pour commencer : betorun login"
