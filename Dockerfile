# syntax=docker/dockerfile:1.7

FROM golang:1.25.6-alpine3.22@sha256:fa3380ab0d73b706e6b07d2a306a4dc68f20bfc1437a6a6c47c8f88fe4af6f75 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN mkdir -p /out/app && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w -buildid=' -o /out/app/skywalking-mirror ./cmd/skywalking-mirror

FROM gcr.io/distroless/static-debian12:nonroot@sha256:b7bb25d9f7c31d2bdd1982feb4dafcaf137703c7075dbe2febb41c24212b946f
COPY --from=build --chown=65532:65532 /out/app /app
WORKDIR /app
USER 65532:65532
EXPOSE 11800 8080
ENTRYPOINT ["/app/skywalking-mirror"]
