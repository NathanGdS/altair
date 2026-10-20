# Build context is the repository root (the consumer imports github.com/nathangds/altair/shared).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/example-consumer ./examples/01-consumer

FROM alpine:3.22
RUN adduser -D -u 10001 consumer
COPY --from=build /out/example-consumer /usr/local/bin/example-consumer
USER consumer
EXPOSE 9090
ENTRYPOINT ["example-consumer"]
