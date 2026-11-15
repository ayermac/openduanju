FROM golang:1.26.2-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Name=openduanju-server -X main.Version=${VERSION}" -o /out/server ./cmd/openduanju-server \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/migrate ./cmd/openduanju-migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/staff-init ./cmd/openduanju-staff-init

FROM alpine:3.20
# ffmpeg and ffprobe are part of the runtime: the media worker transcodes and
# probes the uploaded material inside the container. The Go binary is static,
# so it runs on the alpine base without a C library.
RUN apk add --no-cache ca-certificates ffmpeg && adduser -S -u 10001 -h /home/app app
WORKDIR /app
# The local material, the worker's checkpoints and the SDK profile live under
# one directory that a deployment mounts as a volume.
RUN mkdir -p /var/lib/openduanju/media /var/lib/openduanju/tmp /var/lib/openduanju/hongguo && chown -R 10001:65533 /var/lib/openduanju
COPY --from=builder /out/server /app/server
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/staff-init /app/staff-init
COPY configs/ /app/configs/
USER app
EXPOSE 8000 9000
ENTRYPOINT ["/app/server"]
CMD ["-conf", "/app/configs"]
