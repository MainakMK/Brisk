# Multi-stage build: compile a static binary, ship it on distroless.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /brisk-control ./cmd/brisk-control
# Pre-create the ACME account-key dir so the named volume mounted there inherits
# nonroot ownership (a fresh Docker volume copies the image dir's owner). The agent
# writes /var/lib/brisk-control/acme/<env>/account.key at runtime (managed TLS).
RUN mkdir -p /acme

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /brisk-control /brisk-control
COPY --from=build --chown=nonroot:nonroot /acme /var/lib/brisk-control/acme
EXPOSE 8080
ENTRYPOINT ["/brisk-control"]
