# Multi-stage Dockerfile for Go services
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG SVC=fraud-service
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/service ./cmd/${SVC}

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/bin/service /app/service
COPY configs /app/configs

EXPOSE 8085 8084
ENTRYPOINT ["/app/service"]
