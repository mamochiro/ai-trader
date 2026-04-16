FROM golang:1.25-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/feeder   ./cmd/feeder   && \
    CGO_ENABLED=0 go build -o /bin/signal   ./cmd/signal   && \
    CGO_ENABLED=0 go build -o /bin/executor ./cmd/executor && \
    CGO_ENABLED=0 go build -o /bin/api      ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /bin/ /usr/local/bin/
