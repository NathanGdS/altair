FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/altair ./main.go

FROM alpine:3.22
# Workers assume these exist at startup (see workers.DeleteMakedFiles), and paths are relative
# to the working directory, so they are created under WORKDIR and owned by the runtime user.
RUN adduser -D -u 10001 altair \
 && mkdir -p /app/messages/ready /app/messages/processed /app/messages/trash \
             /app/deliveries/pending /app/deliveries/failed /app/data \
 && chown -R altair:altair /app
WORKDIR /app
COPY --from=build /out/altair /usr/local/bin/altair
COPY web/templates ./web/templates
USER altair
EXPOSE 8080
ENTRYPOINT ["altair"]
