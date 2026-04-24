package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Estructura ligera solo para mapear lo que nos interesa de la API de GitHub
type GitHubWebhook struct {
	ID     int `json:"id"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// DeleteGitHubWebhook busca y elimina el webhook específico de Nubbe.run en el repositorio.
func DeleteGitHubWebhook(ctx context.Context, userID, projectID, repoName, token string) error {
	payloadURL := fmt.Sprintf("https://api.nubbe.run/webhooks/github?uid=%s&pid=%s", userID, projectID)
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/hooks", repoName)

	client := &http.Client{}

	// ==========================================
	// PASO 1: Obtener la lista de webhooks para encontrar el ID
	// ==========================================
	reqGet, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("error creando request GET: %v", err)
	}

	reqGet.Header.Set("Authorization", "token "+token)
	reqGet.Header.Set("Accept", "application/vnd.github.v3+json")

	respGet, err := client.Do(reqGet)
	if err != nil {
		return fmt.Errorf("error ejecutando request GET a GitHub: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respGet.Body)
		return fmt.Errorf("github GET hooks falló con status %d: %s", respGet.StatusCode, string(body))
	}

	var hooks []GitHubWebhook
	if err := json.NewDecoder(respGet.Body).Decode(&hooks); err != nil {
		return fmt.Errorf("error decodificando respuesta de hooks: %v", err)
	}

	// Buscar nuestro hook específico
	var hookIDToDelete int
	for _, hook := range hooks {
		if hook.Config.URL == payloadURL {
			hookIDToDelete = hook.ID
			break
		}
	}

	// Si no encontramos el webhook, no hay nada que borrar (salimos exitosamente)
	if hookIDToDelete == 0 {
		log.Printf("[GitHub] El webhook para %s ya no existe en el repositorio %s", payloadURL, repoName)
		return nil
	}

	// ==========================================
	// PASO 2: Eliminar el webhook usando el ID encontrado
	// ==========================================
	deleteURL := fmt.Sprintf("%s/%d", apiURL, hookIDToDelete)
	reqDel, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("error creando request DELETE: %v", err)
	}

	reqDel.Header.Set("Authorization", "token "+token)
	reqDel.Header.Set("Accept", "application/vnd.github.v3+json")

	respDel, err := client.Do(reqDel)
	if err != nil {
		return fmt.Errorf("error ejecutando request DELETE a GitHub: %v", err)
	}
	defer respDel.Body.Close()

	// GitHub devuelve 204 No Content cuando el borrado es exitoso
	if respDel.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(respDel.Body)
		return fmt.Errorf("github DELETE falló con status %d: %s", respDel.StatusCode, string(body))
	}

	log.Printf("[GitHub] Webhook %d eliminado exitosamente de %s", hookIDToDelete, repoName)
	return nil
}
