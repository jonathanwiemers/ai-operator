# Operator image: the operator (/manager) and the web UI (/ui).

# The web UI's Vue app, embedded into /ui.
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=web /web/dist/ web/dist/
# Declared late so the layers above are shared by all target platforms.
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/ui ./cmd/ui
# The release (YEAR.MONTH.N) the operator reports and compares with the tags
# of --self-project; the release jobs set it, a local build is "dev".
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/manager ./cmd/operator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/manager /manager
COPY --from=build /out/ui /ui
USER 65532:65532
ENTRYPOINT ["/manager"]
