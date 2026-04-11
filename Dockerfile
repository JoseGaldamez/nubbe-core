# Etapa 1: Compilación
FROM golang:1.25.7-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Compilamos estáticamente para que corra en un contenedor scratch
RUN CGO_ENABLED=0 GOOS=linux go build -o /nubbe-core ./cmd/api

# Etapa 2: Producción (Ultra ligera)
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /
COPY --from=builder /nubbe-core /nubbe-core

# Exponemos el puerto estándar
EXPOSE 8080
ENTRYPOINT ["/nubbe-core"]