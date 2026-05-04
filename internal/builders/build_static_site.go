package builders

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Genera el contenido del archivo default.conf para Nginx
func GenerateStaticDockerfile(entryPoint string, advancedConfig map[string]string) string {
	// 1. Leer configuraciones
	cleanUrls := true
	if val, ok := advancedConfig["clean_urls"]; ok && val == "false" {
		cleanUrls = false
	}

	// 2. Manejo de la ruta de la página de error
	errorPage := "/404.html"
	if val, ok := advancedConfig["error_page"]; ok && val != "" {
		errorPage = strings.TrimPrefix(val, ".")
		if !strings.HasPrefix(errorPage, "/") {
			errorPage = "/" + errorPage
		}
	}

	// 3. Construir la configuración de Nginx
	nginxConf := `server {
    listen 80;
    server_name localhost;
    root /usr/share/nginx/html;
    index index.html index.htm;
`
	if cleanUrls {
		// CORRECCIÓN: try_files sin /index.html al final.
		// Si no encuentra el archivo exacto, prueba con .html, luego carpeta, si no, arroja 404.
		nginxConf += `
    location / {
        try_files $uri $uri.html $uri/ =404;
    }`
	} else {
		nginxConf += `
    location / {
        try_files $uri $uri/ =404;
    }`
	}

	// Inyectamos la redirección del error 404
	nginxConf += fmt.Sprintf(`
    error_page 404 %s;
    location = %s {
        internal;
    }`, errorPage, errorPage)
	nginxConf += "\n}"

	// 4. Codificamos la configuración de Nginx en Base64 para evitar errores de sintaxis en el Dockerfile
	encodedNginxConf := base64.StdEncoding.EncodeToString([]byte(nginxConf))

	// 5. Preparar el directorio de origen
	srcDir := entryPoint
	if srcDir == "" || srcDir == "./" {
		srcDir = "."
	}

	// 6. Preparar el HTML por defecto de nubbe.run (ahora con meta charset="UTF-8") y codificarlo también
	fallback404Html := `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>404 - No Encontrado</title><style>body{background-color:#111;color:#fff;font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center;}h1{color:#67e8f9;margin-bottom:8px;}p{color:#9ca3af;}</style></head><body><div><h1>404</h1><p>Esta página no pudo ser encontrada.</p><p style="font-size:1rem;margin-top:24px;color:#4b5563;">Desplegado en <span style="color:#ffffff;font-weight:700;">nubbe<span style="color:#00e5ff;">.run</span></span></p></div></body></html>`
	encodedHtml := base64.StdEncoding.EncodeToString([]byte(fallback404Html))

	// Calculamos la ruta física absoluta de la página 404 dentro del contenedor de Alpine Linux
	physicalPath := "/usr/share/nginx/html" + errorPage

	// 7. Generar el Dockerfile final
	dockerfile := fmt.Sprintf(`FROM nginx:alpine

# Decodificar e inyectar la configuracion de Nginx
RUN echo "%s" | base64 -d > /etc/nginx/conf.d/default.conf

# Copiar los archivos estaticos del usuario
COPY %s /usr/share/nginx/html

# Verificar si existe la pagina de error, si no, crearla usando base64
RUN if [ ! -f "%s" ]; then \
        mkdir -p $(dirname "%s") && \
        echo "%s" | base64 -d > "%s"; \
    fi

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]`,
		encodedNginxConf, srcDir, physicalPath, physicalPath, encodedHtml, physicalPath)

	return dockerfile
}
