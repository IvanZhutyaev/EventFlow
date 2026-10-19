# Multi-stage build for EventFlow binaries (spec §24, §31).
FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/testsink ./cmd/testsink

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /out/migrate /app/migrate
COPY --from=build /out/testsink /app/testsink

# Default binary is overridden per service in docker-compose.
ENTRYPOINT ["/app/api"]