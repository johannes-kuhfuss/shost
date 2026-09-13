FROM golang:1.27.1-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build for scratch image
# CGO_ENABLED=0 --> no dependencies on C libraries
# GOOS=linux --> technically reedundant, but make build target explicit
# -trimpath --> remove local file system path information
# -ldflags="-s -w" --> remove debug information
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/demo-service \
    .

FROM scratch

COPY --from=build /out/demo-service /demo-service

USER 65532:65532

# Pick the correct port, depending on whether or not you use TLS
EXPOSE 8080
#EXPOSE 8443

ENTRYPOINT ["/demo-service"]