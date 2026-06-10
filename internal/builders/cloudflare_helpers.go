package builders

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GenerateCFProjectName crea un nombre seguro y único para el proyecto en Cloudflare Pages.
// Formato: nubbe-run-[subdomain]-[userhash]
func GenerateCFProjectName(subDomain, userID string) string {
	userHash := strings.ToLower(userID)
	if len(userHash) > 6 {
		userHash = userHash[:6]
	}
	safe := subDomain
	if len(safe) > 35 {
		safe = safe[:35]
	}
	return fmt.Sprintf("nubbe-run-%s-%s", safe, userHash)
}

// EnsureCloudflareProject verifica y crea el proyecto en Cloudflare Pages.
// Soporta upsert: si el proyecto ya existe (409 Conflict), retorna nil.
func EnsureCloudflareProject(ctx context.Context, accountID, token, projectName, branch string) error {
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects", accountID)
	payload := fmt.Sprintf(`{"name": "%s", "production_branch": "%s"}`, projectName, branch)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("respuesta inesperada API CF (%d): %s", resp.StatusCode, string(respBody))
}

// RegisterRouteInKV guarda el mapeo del subdominio hacia el destino real en Cloudflare KV.
func RegisterRouteInKV(ctx context.Context, accountID, token, kvNamespaceID, subDomain, cfProjectName string) error {
	key := fmt.Sprintf("%s.nubbe.run", subDomain)
	value := fmt.Sprintf("https://%s.pages.dev", cfProjectName)

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, kvNamespaceID, key)

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(value))
	if err != nil {
		return fmt.Errorf("error creando petición KV: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error ejecutando petición KV: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Cloudflare KV API respondió con error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CheckRouteInKV verifica si un subdominio ya existe registrado en Cloudflare KV.
// Retorna (true, nil) si existe (está ocupado), (false, nil) si no existe (está libre).
func CheckRouteInKV(ctx context.Context, accountID, token, kvNamespaceID, subDomain string) (bool, error) {
	key := fmt.Sprintf("%s.nubbe.run", subDomain)
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, kvNamespaceID, key)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return false, fmt.Errorf("error creando petición GET KV: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("error ejecutando petición GET KV: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, nil // Ocupado
	} else if resp.StatusCode == http.StatusNotFound {
		return false, nil // Libre
	}

	respBody, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("Cloudflare KV API respondió con error (%d): %s", resp.StatusCode, string(respBody))
}

// DeleteRouteInKV elimina el mapeo del subdominio en Cloudflare KV.
func DeleteRouteInKV(ctx context.Context, accountID, token, kvNamespaceID, subDomain string) error {
	key := fmt.Sprintf("%s.nubbe.run", subDomain)
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, kvNamespaceID, key)

	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("error creando petición DELETE KV: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error ejecutando petición DELETE KV: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Cloudflare KV API respondió con error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetCloudflareCredentials obtiene las credenciales de Cloudflare de las variables de entorno.
func GetCloudflareCredentials() (accountID, apiToken, kvNamespaceID string, err error) {
	accountID = os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	apiToken = os.Getenv("CLOUDFLARE_API_TOKEN")
	kvNamespaceID = os.Getenv("CLOUDFLARE_KV_NAMESPACE_ID")

	if accountID == "" || apiToken == "" {
		return "", "", "", fmt.Errorf("faltan credenciales de Cloudflare en las variables de entorno")
	}
	return accountID, apiToken, kvNamespaceID, nil
}

// PackageManagerDetectionScript retorna un script bash que detecta el gestor de paquetes
// (bun, pnpm, yarn, npm) e instala + compila. Es reutilizable por todos los builders de Node.js.
func PackageManagerDetectionScript() string {
	return `
echo "=== Detectando Gestor de Paquetes ==="

if [ -f "bun.lockb" ]; then
    echo "Bun detectado (bun.lockb). Instalando entorno..."
    npm install -g bun
    bun install
    bun run build

elif [ -f "pnpm-lock.yaml" ]; then
    echo "pnpm detectado (pnpm-lock.yaml). Activando corepack..."
    corepack enable pnpm
    pnpm install
    pnpm run build

elif [ -f "yarn.lock" ]; then
    echo "Yarn detectado (yarn.lock). Activando corepack..."
    corepack enable yarn
    yarn install
    yarn build

else
    echo "NPM detectado por defecto."
    if [ -f "package-lock.json" ]; then
        npm ci
    else
        npm install
    fi
    npm run build
fi
`
}

// Nubbe404HTML retorna el HTML por defecto para la página 404 de Nubbe.run
func Nubbe404HTML() string {
	return `<!DOCTYPE html><html><head><meta charset="UTF-8"><title>404 - No Encontrado</title><style>body{background-color:#111;color:#fff;font-family:system-ui,-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;text-align:center;}h1{color:#67e8f9;margin-bottom:8px;}p{color:#9ca3af;}</style></head><body><div><h1>404</h1><p>Esta página no pudo ser encontrada.</p><p style="font-size:1rem;margin-top:24px;color:#4b5563;">Desplegado en <span style="color:#ffffff;font-weight:700;">nubbe<span style="color:#00e5ff;">.run</span></span></p></div></body></html>`
}

// DeleteCloudflareProject elimina el proyecto de Cloudflare Pages.
func DeleteCloudflareProject(ctx context.Context, accountID, token, projectName string) error {
	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/pages/projects/%s", accountID, projectName)

	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("respuesta inesperada API CF al borrar proyecto (%d): %s", resp.StatusCode, string(respBody))
}

