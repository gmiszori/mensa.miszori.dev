# Build stage: compile one static binary. CSS, JS, fonts, templates and the canteen list are
# embedded in it, so nothing else needs to be copied.
# Images are pinned by digest, so a changed upstream image can't slip in under the same tag.
# To update one, look up the new digest (docker buildx imagetools inspect <image>) and
# replace it here.
FROM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mensaplan . && mkdir /data

# Run stage: a minimal image with CA certificates and a non-root user, nothing else.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /mensaplan /mensaplan
COPY --from=build --chown=65532:65532 /data /data
ENV DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/mensaplan"]
