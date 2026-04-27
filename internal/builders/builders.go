package builders

import (
	"fmt"
)

type ProjectBuilder interface {
	GetDockerfile(entryPoint string) string
}

// --- Static Builder ---
type StaticBuilder struct{}

func (b *StaticBuilder) GetDockerfile(entryPoint string) string {
	return `FROM nginx:alpine
COPY . /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]`
}

// --- React Builder ---
type ReactBuilder struct{}

func (b *ReactBuilder) GetDockerfile(entryPoint string) string {
	return `FROM node:20-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]`
}

// --- Astro Builder ---
type AstroBuilder struct{}

func (b *AstroBuilder) GetDockerfile(entryPoint string) string {
	return `FROM node:20-alpine AS build
WORKDIR /app
COPY . .
RUN if [ -f pnpm-lock.yaml ]; then corepack enable && pnpm install --frozen-lockfile; \
    elif [ -f yarn.lock ]; then yarn install --frozen-lockfile; \
    elif [ -f bun.lockb ]; then corepack enable && bun install --frozen-lockfile; \
    else npm install; fi
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]`
}

// --- Node Builder (Refactorizado) ---
type NodeBuilder struct{}

func (b *NodeBuilder) GetDockerfile(entryPoint string) string {
	return fmt.Sprintf(`FROM node:20-alpine
WORKDIR /app
COPY . .
RUN if [ -f pnpm-lock.yaml ]; then corepack enable && pnpm install; \
    elif [ -f yarn.lock ]; then yarn install; \
    elif [ -f bun.lockb ]; then npm install -g bun && bun install; \
    else npm install; fi
EXPOSE 80
CMD ["sh", "-c", "%s"]`, entryPoint)
}

// --- Go Builder ---
type GoBuilder struct{}

func (b *GoBuilder) GetDockerfile(entryPoint string) string {
	return `FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

FROM alpine:latest
WORKDIR /
COPY --from=builder /app/main /main
EXPOSE 80
CMD ["/main"]`
}

// --- Factory ---
func GetBuilder(projectType string) (ProjectBuilder, error) {
	switch projectType {
	case "static":
		return &StaticBuilder{}, nil
	case "react":
		return &ReactBuilder{}, nil
	case "astro":
		return &AstroBuilder{}, nil
	case "nodejs":
		return &NodeBuilder{}, nil
	case "go":
		return &GoBuilder{}, nil
	default:
		return nil, fmt.Errorf("tipo de proyecto no soportado: %s", projectType)
	}
}
