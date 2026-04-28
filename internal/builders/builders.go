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
	return `FROM node:22-alpine AS builder
WORKDIR /app
COPY . .

# 1 y 2. Autodetección del gestor, instalación y compilación unificadas
RUN if [ -f "pnpm-lock.yaml" ]; then \
        echo "Usando pnpm..." && \
        corepack enable && \
        pnpm config set ignore-scripts false && \
        pnpm install --frozen-lockfile && \
        pnpm run build; \
    elif [ -f "yarn.lock" ]; then \
        echo "Usando yarn..." && \
        corepack enable && \
        yarn install --frozen-lockfile && \
        yarn run build; \
    elif [ -f "bun.lockb" ]; then \
        echo "Usando bun..." && \
        npm install -g bun && \
        bun install --frozen-lockfile && \
        bun run build; \
    else \
        echo "Usando npm por defecto..." && \
        npm install && \
        npm run build; \
    fi

# 3. Red de Seguridad: Inyectar 404 por defecto si el usuario no lo creó
RUN if [ ! -f "dist/404.html" ]; then \
        echo '<!DOCTYPE html>
<html>

<head>
    <title>404 - No Encontrado</title>
    <style>
        body {
            background-color: #111;
            color: #fff;
            font-family: system-ui, -apple-system, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            height: 100vh;
            margin: 0;
            text-align: center;
        }

        h1 {
            color: #67e8f9;
            margin-bottom: 8px;
        }

        p {
            color: #9ca3af;
        }
    </style>
</head>

<body>
    <div>
        <h1>404</h1>
        <p>Esta página no pudo ser encontrada.</p>
        <p style="font-size:1rem;margin-top:24px;color:#4b5563;">Desplegado en <span
                style="color: #ffffff; font-weight: 700;">nubbe<span style="color: #00e5ff;">.run</span></span>
        </p>
    </div>
</body>

</html>' > dist/404.html; \
    fi

# 4. Etapa de Producción ultraligera con Nginx
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html

# 5. Configuración maestra de Nginx
RUN cat <<'EOF' > /etc/nginx/conf.d/default.conf
server {
    listen 80;
    root /usr/share/nginx/html;
    index index.html;

    # Capturar el error 404 y servir nuestro archivo
    error_page 404 /404.html;
    location = /404.html {
        internal;
    }

    # Enrutamiento inteligente para "URLs bonitas" de Astro
    location / {
        try_files $uri $uri/ $uri.html /404.html;
    }
}
EOF

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
