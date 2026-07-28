# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build

ARG VERSION=dev
ARG COMMIT=none
ARG BUILT=unknown

WORKDIR /src

# dependency layer — cached until go.mod/go.sum change
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off because modernc.org/sqlite is pure Go
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.built=${BUILT}" \
    -o /out/sangamdrive-api ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -g 10001 -S sangam \
    && adduser -u 10001 -S sangam -G sangam

WORKDIR /app
COPY --from=build /out/sangamdrive-api /app/sangamdrive-api

# the SQLite file lives here; mount a volume over it in production
RUN mkdir -p /data && chown sangam:sangam /data
VOLUME /data

USER sangam
EXPOSE 8080

ENV SQLITE_PATH=/data/sangamdrive.db

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/sangamdrive-api"]
