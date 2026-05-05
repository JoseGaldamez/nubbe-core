package builders

import (
	"context"
	"fmt"
)

// BuildConfig contiene toda la información necesaria para realizar un despliegue.
type BuildConfig struct {
	UserID         string
	SubDomain      string            // Nombre del proyecto o subdominio
	RepoName       string            // Formato "owner/repo"
	GithubToken    string            // Token de acceso de GitHub
	EntryPoint     string            // Ruta de entrada o comando de inicio
	EnvVars        map[string]string // Variables de entorno
	AdvancedConfig map[string]string // Configuraciones específicas adicionales
}

// BuildResult contiene la información de respuesta tras iniciar un despliegue.
type BuildResult struct {
	BuildID       string
	Status        string
	OperationName string
	DeploymentURL string
	Platform      string
}

// Builder es la interfaz que deben implementar todos los tipos de constructores de proyectos.
type Builder interface {
	Deploy(ctx context.Context, config BuildConfig) (*BuildResult, error)
}

// GetBuilderByType es la fábrica que devuelve la estrategia de construcción adecuada.
func GetBuilderByType(projectType string) (Builder, error) {
	switch projectType {
	case "react":
		return NewReactBuilder(), nil
	case "static":
		return NewStaticBuilder(), nil
	case "astro":
		return NewAstroBuilder(), nil
	case "nodejs":
		return NewNodejsBuilder(), nil
	default:
		return nil, fmt.Errorf("tipo de proyecto no soportado para el nuevo flujo: %s", projectType)
	}
}
