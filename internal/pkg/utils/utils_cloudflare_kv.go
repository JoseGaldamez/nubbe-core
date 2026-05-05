package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/run/v1"
)

// fetchCloudRunURL consulta la API de Google para obtener la URL asignada al contenedor
func FetchCloudRunURL(ctx context.Context, subDomain string) (string, error) {
	gcpProjectID := os.Getenv("GCP_PROJECT_ID")
	region := "us-central1" // Asegúrate de que sea la misma región que usaste en node_builder.go

	// Recreamos el nombre del servicio tal como lo generó el builder
	serviceName := strings.ToLower(subDomain)
	if len(serviceName) > 40 {
		serviceName = serviceName[:40]
	}

	// Inicializamos el servicio de Cloud Run
	runService, err := run.NewService(ctx)
	if err != nil {
		return "", fmt.Errorf("fallo al inicializar SDK de Cloud Run: %w", err)
	}

	// El formato de nombre exacto que requiere la API de Google
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", gcpProjectID, region, serviceName)

	// Consultamos el servicio
	svc, err := runService.Projects.Locations.Services.Get(name).Do()
	if err != nil {
		return "", fmt.Errorf("fallo al obtener servicio %s: %w", serviceName, err)
	}

	if svc.Status == nil || svc.Status.Url == "" {
		return "", fmt.Errorf("el servicio se creó pero aún no tiene URL asignada")
	}

	return svc.Status.Url, nil
}

// registerInCloudflareKV guarda el mapeo en tu red Edge
func RegisterInCloudflareKV(ctx context.Context, subDomain string, targetURL string) error {
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	kvNamespaceID := os.Getenv("CLOUDFLARE_KV_NAMESPACE_ID")

	if accountID == "" || token == "" || kvNamespaceID == "" {
		return fmt.Errorf("faltan credenciales de Cloudflare en las variables de entorno")
	}

	// Formato de tu dominio (ej. miprojeto.nubbe.run)
	key := fmt.Sprintf("%s.nubbe.run", subDomain)

	apiURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s",
		accountID, kvNamespaceID, key)

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(targetURL))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "text/plain")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("error de CF API (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
