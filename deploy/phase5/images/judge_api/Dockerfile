FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/judge_api ./cmd/judge_api

# judge_api needs to talk to the Docker daemon to launch sandbox containers.
# We use debian-slim (not distroless) so the Docker socket mount works correctly
# and we can include the Docker CLI if needed for debugging.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=build /out/judge_api /judge_api
EXPOSE 8081
USER nobody
ENTRYPOINT ["/judge_api","--addr",":8081"]
