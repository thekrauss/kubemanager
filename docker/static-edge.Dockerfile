# static-edge : binaire minimal servant les sites statiques (voir cmd/static-edge).
# Alpine plutôt que distroless : le healthcheck a besoin de wget.
FROM golang:1.27.3-alpine3.23 AS build
ARG GOMAXPROCS=
ARG GOMEMLIMIT=
ARG GOFLAGS=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/static-edge ./cmd/static-edge

FROM alpine:3.23.7
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/static-edge /static-edge
USER 10001:10001
EXPOSE 8090 8091
ENTRYPOINT ["/static-edge"]
