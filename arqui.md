# Arquitectura y Reglas de Negocio: API Nubbe.run

Este documento define la arquitectura, modelos de datos y flujos para el orquestador backend de Nubbe.run en Golang. El objetivo es instruir al agente de IA (Gemini CLI) para implementar un sistema asíncrono, seguro y de configuración cero (zero-config).

## 1. Modelo de Datos Esperado (Firebase)

Estructura JSON:

    {
      "project_id": "miapp",
      "subdomain": "miapp",
      "owner_id": "user-uuid",
      "repository": {
        "url": "https://github.com/user/repo",
        "branch": "main"
      },
      "build_config": {
        "target": "CLOUDFLARE_PAGES | CLOUD_RUN",
        "runtime": "nodejs | golang | python | static",
        "runtime_version": "22 | 1.22 | 3.11",
        "build_command": "npm run build",
        "run_command": "npm start",
        "dist_directory": "dist"
      },
      "status": {
        "state": "READY | BUILDING | FAILED",
        "current_url": "https://...",
        "last_deployment_sha": "hash"
      }
    }

## 2. Flujos de Operación

### Flujo 1: Creación de Proyecto, Build Inicial y Enrutamiento (KV)

1. Recepción: El API en Go recibe los metadatos y crea el registro en Firebase.
2. Evaluación de Destino: Determina si el target es CLOUDFLARE_PAGES (estático) o CLOUD_RUN (SSR/Backend).
3. Reserva de Subdominio (Cloudflare KV): Inyecta un registro temporal. KEY: [subdominio].nubbe.run -> VALUE: nubbe.run/deploying-page.
4. Desacoplamiento: Publica un evento en Pub/Sub con el project_id para iniciar el build de forma asíncrona y responde HTTP 200 al cliente.

### Flujo 2: Despliegue de Frontend (Cloudflare Pages)

Aplica para proyectos 100% estáticos.

1. **Extracción Dinámica de Versión (Pre-Build):** El API en Go jamás debe usar versiones estáticas. Al leer el repositorio, debe buscar la versión de Node.js en este orden: `.nvmrc`, `.node-version`, o el campo `engines.node` del `package.json`. Si no existe, aplica una constante de seguridad (ej. `DEFAULT_NODE_LTS = "22"`) y deja un registro de advertencia. El valor normalizado se guarda en Firebase (`build_config.runtime_version`).
2. **Ejecución y Sandbox:** Un worker consume el evento de Pub/Sub y levanta un paso en Cloud Build. El orquestador inyecta la versión de forma dinámica en la imagen del contenedor (ej. `image: node:${RUNTIME_VERSION}`).
3. **Construcción:** Dentro del contenedor, se ejecuta la instalación de dependencias y el `build_command` exacto que fue guardado en Firebase.
4. **Despliegue:** Se utiliza la CLI preinstalada de Cloudflare para subir el artefacto resultante. Comando esperado: `npx wrangler pages deploy [dist_directory] --project-name=[project_id] --commit-dirty=true`.
5. **Actualización de Enrutamiento:** Si el despliegue es exitoso, se actualiza Cloudflare KV de forma estrictamente atómica. KEY: `[subdominio].nubbe.run` -> VALUE: `[url-de-pages.dev]`. Firebase pasa a estado `READY`.

### Flujo 3: Despliegue de Backend y SSR (Cloud Run)

Aplica para Node.js con SSR (Next.js/Astro), Go, Python, Java, ect. En general aplicaciones que necesitan un servidor.

1. Ejecución: El worker consume el evento de Pub/Sub en Cloud Build.
2. Estrategia de Contenedor:
   - Si hay Dockerfile: Ejecuta docker build.
   - Si NO hay Dockerfile: Usa Google Cloud Buildpacks (pack) forzando la imagen base según la versión detectada/configurada y pasando el build_command y run_command.
3. Registro: Sube la imagen a Artifact Registry.
4. Despliegue: Aplica la imagen al servicio en Cloud Run.
5. Actualización de Enrutamiento: Actualiza Cloudflare KV atómicamente. KEY: [subdominio].nubbe.run -> VALUE: [url-de-cloud-run.a.run.app].

### Flujo 4: Webhook desde GitHub (CI/CD)

1. Validación: El API recibe el webhook y valida obligatoriamente la firma X-Hub-Signature-256. Falla inmediatamente si es inválida.
2. Auditoría de Metadatos: Analiza el commit entrante buscando cambios en package.json o go.mod, etc, segun el proyecto, para actualizar automáticamente runtime_version en Firebase si es necesario.
3. Transición de Estado: Marca el proyecto como BUILDING en Firebase.
4. Desacoplamiento: Lanza el evento a Pub/Sub para iniciar el Flujo 2 o Flujo 3.

### Flujo 5: Proceso de Borrado

1. Recepción: El evento de borrado entra por Pub/Sub con prioridad alta.
2. Corte de Tráfico: Elimina inmediatamente la llave en Cloudflare KV para retornar 404 instantáneo.
3. Limpieza en Paralelo:
   - Elimina el servicio en Cloud Run.
   - Elimina el proyecto en Cloudflare Pages.
   - Borra contenedores huérfanos en Artifact Registry.
4. Eliminación de Datos: Borra el documento del proyecto en Firebase.

## 3. Análisis de Errores y Reglas Críticas para el Agente AI

- Riesgo de Bloqueo: El API HTTP principal NUNCA debe esperar a que termine un despliegue. Todo procesamiento pesado debe ir a Pub/Sub. Si se implementa de forma síncrona, el API colapsará por timeouts.
- Falla de Seguridad (RCE): Los comandos definidos por el usuario (build_command, run_command) son un vector de ataque directo. Deben ejecutarse estrictamente dentro del sandbox de Cloud Build. El orquestador en Go jamás debe ejecutar os/exec con estos strings.
- Inconsistencia de Estado en KV: El Worker de CF enruta según el KV. La actualización del KV debe ser estrictamente el paso final tras validar el éxito del despliegue. Hacerlo antes generará falsos positivos y enlaces rotos.
- Opacidad de Errores: Se debe integrar la API de Cloud Logging para enviar feedback en tiempo real al frontend. Omitir esto dejará al usuario sin visibilidad sobre fallos de compilación.
