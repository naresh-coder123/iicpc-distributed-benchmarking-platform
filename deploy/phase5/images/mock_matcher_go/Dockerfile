FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/mock_matcher ./cmd/mock_matcher_go

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/mock_matcher /mock_matcher
EXPOSE 50051
USER 65532:65532
ENTRYPOINT ["/mock_matcher","--addr","0.0.0.0:50051"]

