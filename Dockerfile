FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/esperanta ./cmd/esperanta

FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source="https://github.com/nexryai/esperanta"

COPY --from=build /out/esperanta /usr/bin/esperanta
COPY config.yaml /etc/esperanta/config.yaml

USER 65532:65532
ENTRYPOINT ["/usr/bin/esperanta"]
CMD ["--config", "/etc/esperanta/config.yaml"]
