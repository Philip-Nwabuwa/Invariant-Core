# Multi-stage build for the three long-running Go services (ledger, switchd,
# mockrail). One image holds all three binaries; each Railway service runs the
# same image and overrides the start command (Settings -> Deploy -> Custom Start
# Command) with /app/ledger, /app/switchd, or /app/mockrail.
#
# Generated code (api/gen, sqlc *db.go) is committed, so a plain `go build`
# compiles without buf/sqlc. pgx is pure Go, so the binaries are static
# (CGO disabled) and run on a minimal Alpine base.
# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -o /out/ ./cmd/ledger ./cmd/switchd ./cmd/mockrail

# Alpine (not distroless) so Railway's shell-wrapped Custom Start Command works.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/ /app/
USER app
# Default target; Railway overrides this per service with a Custom Start Command:
#   ledger   -> /app/ledger
#   switchd  -> /app/switchd
#   mockrail -> /app/mockrail
CMD ["/app/switchd"]
