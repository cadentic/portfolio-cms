# syntax=docker/dockerfile:1.7
# Builds the API/worker binary and the local mock email provider.
#   docker build --target api  -t portfolio-api  backend
#   docker build --target mock -t portfolio-mock backend
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockprovider ./cmd/mockprovider

FROM alpine:3.22 AS runtime
# CA roots come from the build stage; time zones are embedded in the binary.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# Default MEDIA_LOCAL_DIR. A named volume mounted here inherits this owner.
RUN mkdir -p /data/media && chown 10001:10001 /data/media
USER 10001:10001

FROM runtime AS mock
COPY --from=build /out/mockprovider /usr/local/bin/mockprovider
EXPOSE 8025
ENTRYPOINT ["/usr/local/bin/mockprovider"]

# Default target: the API. Set RUN_WORKER=false for an API-only container and
# run a second container with RUN_WORKER=true as the queue worker.
FROM runtime AS api
COPY --from=build /out/api /usr/local/bin/api
ENV HTTP_ADDR=:8080 APP_ENV=production
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/api"]
