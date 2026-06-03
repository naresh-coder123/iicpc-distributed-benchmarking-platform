FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/leaderboard_api ./cmd/leaderboard_api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/leaderboard_api /leaderboard_api
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/leaderboard_api","--addr",":8080"]

