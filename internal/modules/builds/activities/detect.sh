# Détection du type de projet, sans Dockerfile : écrit "$GEN/Dockerfile" et
# affiche le type trouvé. Tourne dans le Job de build, sur du code non fiable :
# on ne fait que lire des fichiers, rien du projet n'est exécuté ni évalué ici.
# Tous les modèles écoutent sur 8080 (port par défaut de la plateforme, PORT
# dans l'environnement), sans droits root.

has_file() { [ -f "$CTX/$1" ]; }
json_key() { grep -Eq "\"$1\"[[:space:]]*:" "$CTX/package.json"; }
npm_script() { awk '/"scripts"[[:space:]]*:/{f=1} f{print} f&&/}/{exit}' "$CTX/package.json" | grep -Eq "\"$1\"[[:space:]]*:"; }

node_install() {
  if has_file pnpm-lock.yaml; then echo 'corepack enable pnpm && pnpm install --frozen-lockfile'
  elif has_file yarn.lock; then echo 'corepack enable yarn && yarn install --frozen-lockfile'
  elif has_file package-lock.json; then echo 'npm ci'
  else echo 'npm install'; fi
}

node_run() {
  if has_file pnpm-lock.yaml; then echo "pnpm run $1"
  elif has_file yarn.lock; then echo "yarn run $1"
  else echo "npm run $1"; fi
}

node_head() {
  cat > "$GEN/Dockerfile" <<EOF
FROM node:22-alpine
WORKDIR /app
COPY . .
ENV PORT=8080 HOST=0.0.0.0 HOSTNAME=0.0.0.0 HOME=/tmp NEXT_TELEMETRY_DISABLED=1
RUN $(node_install)
EOF
}

write_static_node() {
  node_head
  sed -i '1s/$/ AS build/' "$GEN/Dockerfile"
  cat >> "$GEN/Dockerfile" <<EOF
RUN $(node_run build)
RUN index="\$(find dist build out public -maxdepth 3 -name index.html 2>/dev/null | sort | head -n 1)"; \\
    if [ -z "\$index" ]; then echo "index.html not found after the build (dist, build, out, public)" >&2; exit 1; fi; \\
    mkdir /site && cp -r "\$(dirname "\$index")/." /site/
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=build /site /usr/share/nginx/html
COPY betorun-nginx.conf /etc/nginx/conf.d/default.conf
EOF
  write_nginx_conf
}

# Application monopage ou site : un chemin inconnu renvoie index.html.
write_nginx_conf() {
  cat > "$CTX/betorun-nginx.conf" <<'EOF'
server {
  listen 8080;
  root /usr/share/nginx/html;
  index index.html;
  location / { try_files $uri $uri/ /index.html; }
}
EOF
}

detect_node() {
  if json_key next; then
    if grep -Eqs "output[[:space:]]*:[[:space:]]*['\"]export['\"]" "$CTX/next.config.js" "$CTX/next.config.mjs" "$CTX/next.config.ts"; then
      write_static_node; echo next-static; return
    fi
    node_head
    cat >> "$GEN/Dockerfile" <<EOF
RUN $(node_run build)
RUN chown -R 1000:1000 .next
USER 1000:1000
EXPOSE 8080
CMD ["npx", "next", "start", "-p", "8080", "-H", "0.0.0.0"]
EOF
    echo next; return
  fi
  # Outils qui produisent des fichiers statiques : même avec un script start
  # (serveur de développement), on publie le résultat du build.
  if npm_script build && { json_key vite || json_key react-scripts || json_key @vue/cli-service || json_key @angular/core || json_key astro || json_key gatsby; } \
     && ! json_key @sveltejs/kit && ! json_key nuxt && ! json_key @remix-run/node; then
    write_static_node; echo static-build; return
  fi
  if npm_script start; then
    node_head
    if npm_script build; then echo "RUN $(node_run build)" >> "$GEN/Dockerfile"; fi
    cat >> "$GEN/Dockerfile" <<EOF
USER 1000:1000
EXPOSE 8080
CMD ["npm", "start"]
EOF
    echo node; return
  fi
  if npm_script build; then write_static_node; echo static-build; return; fi
  for entry in server.js index.js app.js main.js; do
    if has_file "$entry"; then
      node_head
      cat >> "$GEN/Dockerfile" <<EOF
USER 1000:1000
EXPOSE 8080
CMD ["node", "$entry"]
EOF
      echo node; return
    fi
  done
  return 1
}

py_head() {
  cat > "$GEN/Dockerfile" <<EOF
FROM python:3.12-slim
WORKDIR /app
COPY . .
ENV PORT=8080 HOME=/tmp PYTHONUNBUFFERED=1
EOF
  if has_file requirements.txt; then echo 'RUN pip install --no-cache-dir -r requirements.txt' >> "$GEN/Dockerfile"
  elif has_file pyproject.toml; then echo 'RUN pip install --no-cache-dir .' >> "$GEN/Dockerfile"; fi
}

py_tail() {
  [ -n "$1" ] && echo "RUN pip install --no-cache-dir $1" >> "$GEN/Dockerfile"
  cat >> "$GEN/Dockerfile" <<EOF
USER 1000:1000
EXPOSE 8080
CMD $2
EOF
}

# "module:variable" de l'application, cherchée dans les emplacements usuels.
py_app() {
  for f in main.py app.py server.py wsgi.py asgi.py api.py app/main.py src/main.py; do
    has_file "$f" || continue
    var="$(sed -n "s/^\([A-Za-z_][A-Za-z0-9_]*\)[[:space:]]*=[[:space:]]*\(fastapi\.\|flask\.\)\{0,1\}$1(.*/\1/p" "$CTX/$f" | head -n 1)"
    if [ -n "$var" ]; then echo "$(echo "${f%.py}" | tr / .):$var"; return 0; fi
  done
  return 1
}

py_deps() { cat "$CTX/requirements.txt" "$CTX/pyproject.toml" 2>/dev/null; }

detect_python() {
  if has_file manage.py; then
    module="$(sed -n "s/.*DJANGO_SETTINGS_MODULE[^a-zA-Z0-9_]*\([A-Za-z0-9_]*\)\.settings.*/\1/p" "$CTX/manage.py" | head -n 1)"
    if [ -n "$module" ]; then
      py_head; py_tail gunicorn "[\"gunicorn\", \"$module.wsgi:application\", \"--bind\", \"0.0.0.0:8080\"]"
      echo django; return
    fi
  fi
  if py_deps | grep -iq fastapi && app="$(py_app FastAPI)"; then
    py_head; py_tail "uvicorn" "[\"uvicorn\", \"$app\", \"--host\", \"0.0.0.0\", \"--port\", \"8080\"]"
    echo fastapi; return
  fi
  if py_deps | grep -iq flask && app="$(py_app Flask)"; then
    py_head; py_tail gunicorn "[\"gunicorn\", \"$app\", \"--bind\", \"0.0.0.0:8080\"]"
    echo flask; return
  fi
  if py_deps | grep -iq streamlit; then
    for f in streamlit_app.py app.py main.py; do
      if has_file "$f"; then
        py_head; py_tail "" "[\"streamlit\", \"run\", \"$f\", \"--server.port\", \"8080\", \"--server.address\", \"0.0.0.0\", \"--server.headless\", \"true\"]"
        echo streamlit; return
      fi
    done
  fi
  for f in main.py app.py server.py; do
    if has_file "$f"; then
      py_head; py_tail "" "[\"python\", \"$f\"]"
      echo python; return
    fi
  done
  return 1
}

detect_project() {
  if has_file package.json; then detect_node && return; fi
  if has_file requirements.txt || has_file pyproject.toml || has_file manage.py || has_file main.py || has_file app.py; then detect_python && return; fi
  if has_file index.html; then
    cat > "$GEN/Dockerfile" <<'EOF'
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY . /usr/share/nginx/html
COPY betorun-nginx.conf /etc/nginx/conf.d/default.conf
EOF
    write_nginx_conf
    echo static; return
  fi
  return 1
}
