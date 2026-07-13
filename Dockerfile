# syntax=docker/dockerfile:1
FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cloudflare-ddns ./cmd/cloudflare-ddns \
    && mkdir -p /out/data \
    && chown 65532:65532 /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/cloudflare-ddns /cloudflare-ddns
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/cloudflare-ddns"]
CMD ["agent"]
