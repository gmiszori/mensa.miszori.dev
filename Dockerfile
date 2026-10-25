# Build stage: compile one static binary. CSS, JS, fonts, templates and the canteen list are
# embedded in it, so nothing else needs to be copied.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mensaplan . && mkdir /data

# Run stage: a minimal image with CA certificates and a non-root user, nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /mensaplan /mensaplan
COPY --from=build --chown=65532:65532 /data /data
ENV DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/mensaplan"]
