# Task List: Nubbe Core Alignment (no realizarlas, ya fueron implementadas)

Este documento detalla las tareas necesarias para alinear el proyecto `nubbe-core` con la arquitectura y reglas de negocio definidas en `arqui.md`.

## Fase 1: Refactorización de Modelos y Repositorio (Capa de Datos)

- [x] **Actualizar Modelo de Proyecto:** Modificar la estructura de datos en `internal/repository/project_repository.go` para que coincida con el esquema JSON definido en `arqui.md` (incluyendo `build_config` y `status` anidados).
- [x] **Migración de Firebase:** Asegurar que las funciones de lectura/escritura en `project_repository.go` manejen la nueva estructura anidada.

## Fase 2: Implementación de Comunicación Asíncrona (Pub/Sub)

- [x] **Paquete Pub/Sub:** Crear un paquete en `internal/pkg/pubsub` para gestionar la publicación de eventos.
- [x] **Desacoplamiento de Creación:** Modificar `HandleCreateProject` para que, tras guardar en Firebase y reservar el subdominio en KV, publique un mensaje en Pub/Sub y responda inmediatamente (200 OK).
- [x] **Desacoplamiento de Webhooks:** Modificar el handler de webhooks de GitHub para que valide la firma, actualice el estado a `BUILDING` y publique en Pub/Sub en lugar de disparar el build sincrónicamente.

## Fase 3: Seguridad y Validación (Webhooks)

- [x] **Validación de Firma GitHub:** Implementar la validación obligatoria de la firma `X-Hub-Signature-256` en `internal/handlers/webhook.go`.
- [x] **Auditoría de Metadatos:** Implementar la lógica para analizar el commit entrante y actualizar `runtime_version` automáticamente si cambian archivos de dependencias (`package.json`, `go.mod`).

## Fase 4: Orquestación de Despliegue (Workers)

- [x] **Worker de Build:** Crear un worker (puede ser un nuevo binario o parte del API) que consuma eventos de Pub/Sub.
- [x] **Lógica de Buildpacks:** Integrar Google Cloud Buildpacks en el proceso de build para proyectos sin `Dockerfile`, inyectando `build_command` y `run_command` dinámicamente.
- [x] **Gestión de Enrutamiento (KV):** Refinar la lógica de actualización de Cloudflare KV para que sea atómica y ocurra estrictamente al final del despliegue exitoso.

## Fase 5: Resiliencia y Monitoreo

- [x] **Graceful Shutdown:** Implementar el cierre ordenado en `cmd/api/main.go` para manejar señales `SIGINT`/`SIGTERM`.
- [x] **Integración de Logs:** Asegurar que los errores de build se envíen a Cloud Logging para feedback en tiempo real.

## Fase 6: Pruebas y Validación

- [x] **Pruebas Unitarias:** Añadir tests para los builders y la lógica de validación de webhooks.
- [x] **Pruebas de Integración:** Validar el flujo completo desde la creación hasta la actualización de KV.
