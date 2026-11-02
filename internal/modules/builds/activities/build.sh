set -e
SRC=/home/user/.local/share/buildkit/source
GEN=/tmp/generated
mkdir -p "$SRC" "$GEN"

if [ -n "$BUILD_GIT_URL" ]; then
  echo "==> source: $BUILD_GIT_URL ${BUILD_GIT_REF:-} ${BUILD_GIT_COMMIT:-}"
  # Dépôt privé : le jeton part dans un en-tête HTTP (jamais dans l'URL ni dans
  # .git/config), uniquement pour les commandes git ci-dessous.
  if [ -n "${BUILD_GIT_TOKEN:-}" ]; then
    GIT_CONFIG_COUNT=1
    GIT_CONFIG_KEY_0=http.extraHeader
    GIT_CONFIG_VALUE_0="Authorization: Basic $(printf 'x-access-token:%s' "$BUILD_GIT_TOKEN" | base64 | tr -d '\n')"
    GIT_TERMINAL_PROMPT=0
    export GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0 GIT_TERMINAL_PROMPT
  fi
  if [ -n "${BUILD_GIT_COMMIT:-}" ]; then
    git init --quiet "$SRC"
    git -C "$SRC" -c core.hooksPath=/dev/null fetch --quiet --depth 1 --no-tags -- "$BUILD_GIT_URL" "$BUILD_GIT_COMMIT"
    git -C "$SRC" -c core.hooksPath=/dev/null -c advice.detachedHead=false checkout --quiet FETCH_HEAD
  elif [ -n "$BUILD_GIT_REF" ]; then
    git clone --quiet --depth 1 --no-tags --single-branch --branch "$BUILD_GIT_REF" -c core.hooksPath=/dev/null -- "$BUILD_GIT_URL" "$SRC"
  else
    git clone --quiet --depth 1 --no-tags --single-branch -c core.hooksPath=/dev/null -- "$BUILD_GIT_URL" "$SRC"
  fi
  unset BUILD_GIT_TOKEN GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0
  rm -rf "$SRC/.git"
else
  wget -q -O - "$BUILD_SOURCE_URL" | tar -xz -C "$SRC"
fi
CTX="$SRC${BUILD_CONTEXT_DIR:+/$BUILD_CONTEXT_DIR}"
if [ ! -d "$CTX" ]; then
  echo "folder not found in the source: $BUILD_CONTEXT_DIR" >&2
  exit 1
fi

KIND=dockerfile
DF_DIR="$CTX"
DF_NAME="$BUILD_DOCKERFILE"
if [ ! -f "$CTX/$BUILD_DOCKERFILE" ]; then
  if [ "$BUILD_DOCKERFILE" != "Dockerfile" ]; then
    echo "file not found in the source: $BUILD_DOCKERFILE" >&2
    exit 1
  fi
  if ! KIND="$(detect_project)"; then
    echo "betorun:no-build-plan" > /dev/termination-log
    echo "project type not recognized: no Dockerfile, package.json, Python project or index.html" >&2
    exit 1
  fi
  echo "==> no Dockerfile: project detected as $KIND"
  DF_DIR="$GEN"
  DF_NAME=Dockerfile
  [ -f "$CTX/.dockerignore" ] || printf 'node_modules\n.git\n.env\n.env.*\n__pycache__\n.venv\n' > "$CTX/.dockerignore"
fi

# Sans build réussi précédent, pas de cache à importer : l'essayer afficherait
# une erreur inoffensive, mais inquiétante, dans le journal.
set --
[ -z "$BUILD_IMPORT_CACHE" ] || set -- --import-cache "type=registry,ref=$BUILD_CACHE$BUILD_REGISTRY_OPTS"
buildctl-daemonless.sh build --frontend dockerfile.v0 --progress=plain \
  --local "context=$CTX" --local "dockerfile=$DF_DIR" --opt "filename=$DF_NAME" \
  --output "type=image,name=$BUILD_IMAGE,push=true$BUILD_REGISTRY_OPTS" \
  --export-cache "type=registry,mode=max,ref=$BUILD_CACHE$BUILD_REGISTRY_OPTS" \
  "$@" --metadata-file /tmp/metadata.json
DIGEST="$(sed -n 's/.*"containerimage.digest": *"\(sha256:[a-f0-9]*\)".*/\1/p' /tmp/metadata.json)"
printf '%s\n%s\n' "$DIGEST" "$KIND" > /dev/termination-log
