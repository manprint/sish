FROM --platform=$BUILDPLATFORM golang:1.26.1-alpine AS builder
LABEL maintainer="Antonio Mika <me@antoniomika.me>"

ENV CGO_ENABLED=0

WORKDIR /app

RUN mkdir -p /emptydir
RUN apk add --no-cache git ca-certificates tzdata

COPY go.* ./

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go mod download

FROM builder AS build-image

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG REPOSITORY=unknown

ARG TARGETOS
ARG TARGETARCH

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go build -o /go/bin/app -ldflags="-s -w -X github.com/${REPOSITORY}/cmd.Version=${VERSION} -X github.com/${REPOSITORY}/cmd.Commit=${COMMIT} -X github.com/${REPOSITORY}/cmd.Date=${DATE}"

ENTRYPOINT ["/go/bin/app"]

FROM alpine:3.23 AS release
LABEL maintainer="Antonio Mika <me@antoniomika.me>"

RUN apk add --no-cache ca-certificates tzdata nano wget

ENV TZ=Europe/Rome

WORKDIR /app

COPY --from=build-image /app/deploy/ /app/deploy/
COPY --from=build-image /app/templates /app/templates
COPY --from=build-image /go/bin/ /app/

ENTRYPOINT ["/app/app"]
