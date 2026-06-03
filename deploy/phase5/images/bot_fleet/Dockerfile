FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/bot_fleet ./cmd/bot_fleet

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/bot_fleet /bot_fleet
USER 65532:65532
ENTRYPOINT ["/bot_fleet"]
