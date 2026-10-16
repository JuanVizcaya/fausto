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

# Fausto runs git to list files and to build proposal commits.
FROM alpine:3.22 AS runtime
RUN apk add --no-cache git
COPY --from=build /out/fausto /usr/local/bin/fausto
# git needs a writable HOME; the repository is mounted at /repo.
ENV HOME=/tmp FAUSTO_ROOT=/repo FAUSTO_ADDR=0.0.0.0:7070
EXPOSE 7070
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO /dev/null http://127.0.0.1:7070/api/info || exit 1
ENTRYPOINT ["fausto"]
CMD ["serve"]
