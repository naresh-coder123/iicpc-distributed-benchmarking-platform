FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/telemetry_ingester ./cmd/telemetry_ingester

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/telemetry_ingester /telemetry_ingester
USER 65532:65532
ENTRYPOINT ["/telemetry_ingester"]

