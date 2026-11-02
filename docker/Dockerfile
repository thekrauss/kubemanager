# Image de l'API et du worker (APP_MODE=worker). Configuration uniquement par
# l'environnement : aucun fichier de config ni secret dans l'image. Le chart
# Helm et les catalogues sont embarqués dans le binaire.
#
# Sur une petite machine, borner la compilation :
#   docker build --build-arg GOMAXPROCS=3 --build-arg GOMEMLIMIT=3GiB --build-arg GOFLAGS=-p=3 ...
FROM golang:1.27.3-alpine3.23 AS build
ARG GOMAXPROCS=
ARG GOMEMLIMIT=
ARG GOFLAGS=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kubemanager ./cmd/gateway

FROM alpine:3.23.7
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app -h /app app
WORKDIR /app
COPY --from=build /out/kubemanager ./kubemanager
COPY migrations ./migrations
# HOME : caches de Helm et du client Kubernetes.
ENV HOME=/tmp
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/kubemanager"]
