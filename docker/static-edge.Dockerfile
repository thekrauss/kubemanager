# static-edge : binaire minimal servant les sites statiques (voir cmd/static-edge).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/static-edge ./cmd/static-edge

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/static-edge /static-edge
EXPOSE 8090 8091
USER nonroot:nonroot
ENTRYPOINT ["/static-edge"]
