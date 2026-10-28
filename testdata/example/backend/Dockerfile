# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS development
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM development AS test
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test -race -count=1 ./...

FROM development AS build
# SERVICE selects the command under cmd/ to build: habbyte, or an extracted
# module later.
ARG SERVICE=habbyte
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/$SERVICE

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/service /service
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/service"]
