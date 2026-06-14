FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/judge_api ./cmd/judge_api

# Using distroless — Docker socket is NOT mounted on AKS (containerd nodes).
# judge_api runs in registry-only mode when the Docker daemon is unreachable.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/judge_api /judge_api
EXPOSE 8081
USER 65532:65532
ENTRYPOINT ["/judge_api", "--addr", ":8081"]
