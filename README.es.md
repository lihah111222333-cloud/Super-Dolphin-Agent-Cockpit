# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**El repositorio autoprotegido para software escrito por IA.** Los agentes de IA implementan los cambios; los maps, contracts, tests y gates que pertenecen al repositorio deciden si esos cambios son lo bastante seguros para conservarse.

> [!IMPORTANT]
> **Declaración de los mantenedores: el código original y la documentación propia del proyecto están escritos al 100% por IA, dirigidos por personas y protegidos por el repositorio.** El código de producto, el código de tests y la documentación propia del proyecto son escritos o refactorizados por agentes de IA. Las personas conservan la responsabilidad sobre la intención del producto, las decisiones de arquitectura, las credenciales y las publicaciones. Que la autoría sea de IA no implica infalibilidad: todo cambio aceptado sigue sujeto a la evidencia y los gates que pertenecen al repositorio. Los textos legales y comunitarios procedentes de terceros conservan su atribución original.

Super Dolphin Agent es un **plano de control para gobernanza de software y desarrollo multiagente nativo de IA**. Reúne un runtime de escritorio local, orquestación MCP, navegación LSP multilenguaje, integraciones con Providers, workflows persistentes y límites de ingeniería aplicados por máquina en una implementación de referencia funcional.

El [README.md](README.md) en inglés es la descripción normativa. Las traducciones conservan el mismo alcance del producto, comandos, rutas, variables de entorno, identidad del repositorio y licencia. Los detalles se encuentran en [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md) y el [Code Map](docs/doc/codemap/README.md) generado.

<!-- sd:why -->
## Por qué existe Super Dolphin

La mayoría de los Agent frameworks optimizan la ejecución de tareas. Super Dolphin también gobierna qué puede modificar una tarea completada dentro de un sistema de software de larga duración.

Su ciclo de mantenimiento tiene cinco etapas:

1. **Orientar** mediante code maps generados y capability contracts.
2. **Comprender** definiciones, referencias, jerarquías de llamadas y diagnósticos mediante LSP.
3. **Cambiar** una superficie acotada y con ownership explícito.
4. **Restringir** el diff mediante reglas AST/SSA, límites de dependencias, presupuestos de complejidad y contratos fail-fast.
5. **Demostrar** el resultado mediante tests focalizados, comprobaciones de artefactos generados y gates sensibles al cambio.

### Mantenimiento con contexto acotado

El repositorio está diseñado para que los cambios habituales no requieran cargar todo el código fuente en un único contexto del modelo. La navegación generada, los contracts estrechos y los fallos deterministas ayudan al agente a encontrar la superficie relevante y reparar las infracciones con rapidez.

Esto no garantiza que todos los cambios sean locales. El trabajo transversal sigue exigiendo un análisis más amplio de referencias e impacto, y todo cambio aceptado continúa sujeto a los tests y la evidencia de revisión correspondientes.

### Origen: afrontar el AI code rot

Super Dolphin nació el 19 de marzo de 2026 como sucesor desde cero de `go-agent-v2`, un prototipo privado que combinaba workflows automatizados de trading cuantitativo con controles de escritorio multiagente. Según los registros de los mantenedores anteriores a la publicación, el prototipo funcionaba, pero las restricciones blandas por sí solas hicieron que su arquitectura fuese cada vez más difícil de razonar:

- más de 80 métodos RPC acumularon rutas paralelas de binding, validation, capability y logging;
- el ownership del lifecycle se dispersó entre managers y efectos secundarios asíncronos;
- un event handler central alcanzó 557 líneas;
- el ensamblado manual de la aplicación superó las 200 líneas.

Llamamos a esto **AI code rot**: los cambios locales siguen funcionando mientras se degradan los contracts globales, los límites de ownership y la legibilidad. El historial privado no constituye evidencia pública; en su lugar, el repositorio público expone los guards, los fixtures de regresión y los comandos reproducibles que surgieron de esos problemas.

| Modo de fallo de V2 | Respuesta de Super Dolphin |
|---|---|
| Rutas RPC manuales y paralelas | Requests tipadas, una única superficie de contract, middleware explícito y semántica de errores |
| Efectos del lifecycle distribuidos | Transiciones declarativas, eventos tipados y lifecycle runners con ownership definido |
| Grafos de objetos manuales | Composición con `fx` y ownership explícito del inicio y el cierre |
| Código de negocio acoplado a adapters | Límites onion, ports propiedad del módulo y adapters anticorrupción |
| Funciones gigantes con niveles de abstracción mezclados | Presupuesto `80 / 4 / 10` de longitud, anidamiento y complejidad específico de este repositorio |
| La memoria del reviewer como política | Guards AST/SSA, maps generados, manifests, hooks y evidencia reproducible |

El presupuesto `80 / 4 / 10` no es una regla de estilo universal. Es una restricción que se endurece progresivamente para este repositorio con un uso intensivo de orquestación: longitud efectiva de función `<= 80`, anidamiento `<= 4` y complejidad ciclomática `<= 10` de forma predeterminada.

### Qué aplica el repositorio

| Capa | Riesgo que evita | Evidencia en el repositorio |
|---|---|---|
| Verdad de navegación | Editar el subsistema equivocado o utilizar conocimiento obsoleto del proyecto | `docs/doc/codemap`, project map, capability manifest |
| Límites de arquitectura | Que el código de dominio acceda a implementaciones de Store, Provider, UI o Command | Registro tipado de límites del backend y evaluación de imports mediante AST |
| Guards semánticos | Errores ignorados, silent fallback, rutas inseguras del lifecycle y propagación de servicios amplios | Guards AST y análisis SSA prioritario |
| Ratchets de complejidad | Que el código nuevo aumente la deuda estructural conocida | Particiones de función, anidamiento, complejidad y freeze de production/tests |
| Evidencia de aceptación | Tratar el estado «done» de un agente como prueba | Tests focalizados, comprobaciones de estado generado, Git hooks y gates sensibles al cambio |

### Casos respaldados por el historial

Los mantenedores registraron cinco incidentes anteriores a la publicación que ahora disponen de evidencia pública de regresión: scope LSP del worktree equivocado, ausencia de Provider identity, ausencia de runtime truth para un agente persistente, fallos asíncronos de UI silenciados y un bypass mediante type alias en un guard de arquitectura.

Consulta el límite entre incidente y evidencia y ejecuta todas las pruebas conservadas en [Governance in Action](docs/open-source/GOVERNANCE.md).

### Por qué no es otro Agent framework

| Agent framework habitual | Super Dolphin Agent |
|---|---|
| Optimiza la ejecución de tareas | Gobierna cómo las tareas cambian un sistema de software real |
| Proporciona más herramientas y contexto a los agentes | Proporciona contexto acotado y direcciones de dependencia permitidas |
| Considera que un run completado es un éxito | Exige tests, diagnósticos, comprobaciones del estado generado y evidencia de Git |
| Depende principalmente de la disciplina del prompt | Aplica invariantes mediante código, tests, hooks y manifests generados |
| Oculta la ausencia de estado mediante retries o defaults | Falla de inmediato si faltan configuración, identidad, ownership o dependencias |

<!-- sd:architecture -->
## Arquitectura

```text
frontend-app/             React/Vite desktop UI
        |
cmd/agent-terminal/       Wails host and RPC boundary
        |
internal/app/             composition and anti-corruption adapters
        |
internal/contract/        stable ports and DTOs
        |
internal/module/          business capabilities
   |             |
internal/store/   internal/provider/
SQLite/sqlc       Codex and provider runtime integration

cmd/mcp-lsp/              generic multi-language LSP peer
cmd/mcp-orch/             orchestration, DAG, cron, and agent tools
```

La regla de dependencias principal es el ownership hacia dentro: los módulos definen los ports que necesitan y los adapters implementan esos ports; los paquetes Platform y Provider no deben importar hacia arriba los módulos de negocio. El registro de límites del backend es la única fuente utilizada para generar el mapa de reglas de arquitectura.

Consulta [Architecture](docs/open-source/ARCHITECTURE.md) para conocer las responsabilidades de los componentes, el flujo de datos, las fuentes de verdad y el alcance conocido. Utiliza el [Code Map](docs/doc/codemap/README.md) generado para la navegación por archivos.

### Alcance actual

- La aplicación de escritorio y su ciclo de gobernanza específico para este repositorio están implementados aquí.
- `make guard` y las comprobaciones relacionadas gobiernan este repositorio; no se anuncian como un scanner general para repositorios arbitrarios.
- La política de código fuente público y las primitivas de validación incluidas son fundamentos para preparar la publicación. Un CLI completo de source export, un workflow de sealed receipts, el gate de CI público y una distribución independiente de los guards aún no son capacidades publicadas.
- La URL canónica de GitHub usada en esta documentación es el destino de publicación. Los enlaces de clone, Issues y reporte privado funcionarán después de que el propietario complete el release checklist.
- Codex es obligatorio para el flujo actual del Provider de escritorio. Claude solo se utiliza en trabajos dirigidos expresamente a la integración con su Provider.

<!-- sd:quick-start -->
## Inicio rápido

### Requisitos previos

- Go 1.25.7
- Node.js 20+ y npm
- OpenAI Codex CLI (`codex`), instalado y autenticado
- `gopls`
- `typescript-language-server` y TypeScript 5.9.3

El comando de clone siguiente apunta al repositorio público canónico y funcionará después de la publicación. Hasta entonces, los mantenedores actuales deben usar su checkout autorizado.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

Ejecuta el flujo actual de desarrollo de escritorio:

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite se crea automáticamente en `SUPER_DOLPHIN_HOME/super-dolphin.db`. Define `SUPER_DOLPHIN_SQLITE_PATH` para utilizar otro archivo local. Las variables de entorno de PostgreSQL no son una vía de configuración de la base de datos del producto.

Compila y ejecuta los tests:

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Quienes contribuyan utilizando Git worktrees enlazados deben compilar y verificar el peer LSP local del worktree antes de editar. Consulta [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness) para obtener los comandos exactos.

<!-- sd:governance-demo -->
## Prueba reproducible de gobernanza

Consulta los gates seleccionados para un archivo cambiado explícito sin ejecutarlos:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

Ejecuta las comprobaciones principales de gobernanza del repositorio:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

Estos comandos validan las reglas de arquitectura, el comportamiento de los guards, la navegación generada, el drift del project map y el capability manifest. Se aplican a este repositorio y fallan si la fuente de verdad está obsoleta, en vez de actualizarla silenciosamente. Utiliza los targets `*-refresh` explícitos solo cuando la fuente propietaria haya cambiado de forma intencionada.

## Calidad del código

| Métrica | Fuente de verdad actual |
|---|---|
| Tests de arquitectura | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Reglas de arquitectura | [Mapa generado de límites del backend](docs/doc/codemap/13-archtest-boundaries.md) |
| Cobertura de tests | Se recalcula a partir de una ejecución actual; no se declara un porcentaje estático |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## Seguridad

No incluyas credenciales, Provider homes, bases de datos locales, logs, memoria de usuarios ni configuración específica del equipo en un commit. La ausencia de identidad, ownership, configuración o dependencias debe fallar de forma cerrada en lugar de degradarse silenciosamente.

Informa de las vulnerabilidades mediante el proceso privado descrito en la [Security Policy](SECURITY.md). No publiques detalles del exploit, secrets, trace payloads ni datos de usuarios en un Issue público.

<!-- sd:community -->
## Comunidad y contribuciones

Los Issues y Pull Requests con un objetivo claro son bienvenidos. Empieza por:

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

Las contribuciones asistidas por IA son bienvenidas, pero quien contribuye sigue siendo responsable del diff enviado, los tests, la seguridad, las licencias y la evidencia. Una respuesta generada o un run correcto de un agente no sustituye los gates del repositorio.

## Licencia

Distribuido bajo la [Apache License 2.0](LICENSE). Consulta [NOTICE](NOTICE) para conocer las directrices de atribución del proyecto y de terceros.
