# syntax=docker/dockerfile:1
# Fausto: drift checks and the web UI for reviewing and proposing
# changes to system/. The web UI is prebuilt into internal/server/dist.
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/fausto ./cmd/fausto

# Fausto runs git to list files, build proposal commits and clone projects.
FROM alpine:3.22 AS runtime
RUN apk add --no-cache git && mkdir -p /data /repo && chown 1000:1000 /data
COPY --from=build /out/fausto /usr/local/bin/fausto
# git needs a writable HOME. /data keeps fausto's clones of GitHub
# projects. A one-off command works on the project mounted at /repo:
#   docker run --rm -v "$PWD":/repo ghcr.io/juanvizcaya/fausto check
ENV HOME=/tmp FAUSTO_DATA=/data FAUSTO_ADDR=0.0.0.0:7070
WORKDIR /repo
EXPOSE 7070
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO /dev/null http://127.0.0.1:7070/api/projects || exit 1
ENTRYPOINT ["fausto"]
CMD ["serve"]
