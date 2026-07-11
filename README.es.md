# Super Dolphin Agent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**Gobernanza de software nativa de IA y plano de control para desarrollo multiagente.**

Super Dolphin Agent está diseñado para proyectos de software mantenidos principalmente por agentes de IA. Integra sesiones multiagente, ejecución de herramientas, orquestación MCP, LSP multilenguaje, planificación, memoria, skills nativas del Provider, eventos en tiempo real y límites de ingeniería aplicados por máquina en un único plano de control de escritorio.

El [README.md](README.md) en inglés es la fuente normativa. Si existe alguna diferencia en el significado del producto, los comandos, las rutas, las variables de entorno, los rule IDs o la licencia, prevalece la versión inglesa.

<!-- sd:why -->
## Diseñado para mantenimiento por IA

«Mantenido por IA» no significa código sin revisión ni que una IA deba comprender todo el repositorio a la vez. Significa que la IA es la principal fuerza de implementación, mientras el repositorio aporta orientación, restricciones y pruebas para que cada cambio sea pequeño y auditable. Las personas siguen siendo responsables de la intención del producto, las decisiones de alto impacto, las credenciales y las publicaciones.

El ciclo de mantenimiento se basa en contexto acotado:

1. Orientarse con code maps generados y un AI project map por archivo.
2. Comprender el comportamiento mediante capability contracts y convenciones de arquitectura explícitas.
3. Modificar una superficie pequeña usando definiciones, referencias, call hierarchies y diagnostics de LSP.
4. Restringir el cambio con reglas AST, SSA, direcciones de dependencia, presupuestos de complejidad y políticas fail-fast.
5. Demostrar el resultado con pruebas enfocadas, comprobaciones de artefactos generados y gates sensibles al cambio antes de commit o push.

Así se elimina la frágil suposición de que una persona o una IA debe mantener todo el código en memoria.

### Origen: de la entropía de V2 a Super Dolphin

Super Dolphin Agent nació el 19 de marzo de 2026 como una migración limpia desde `go-agent-v2`. V2 ya había demostrado el producto: funcionaban las sesiones de agentes, las herramientas, los Providers, los eventos, la recuperación y la experiencia de escritorio. El problema no era la falta de funciones, sino que cada mejora local reducía gradualmente la legibilidad global.

V2 acumuló más de 80 métodos RPC escritos a mano, con binding, validation, capability checks, logging, error mapping y rutas de registro paralelas repartidas por el sistema. La verdad del lifecycle quedó dividida entre varios archivos manager, un estado efectivo superpuesto, una máquina de estados lateral implícita y efectos de recuperación asíncronos. Un event handler central llegó a 557 líneas; el bus creció hasta decenas de constantes message/topic; el ensamblado manual de la aplicación superó las 200 líneas. El código seguía funcionando, pero cada vez era más difícil responder «¿dónde está el comportamiento autoritativo?».

Esto es lo que el proyecto llama **corrupción de software**: no culpa a los desarrolladores ni implica un fallo inmediato. Describe un sistema cuyas piezas locales funcionan mientras contracts, ownership y límites de cambio se convierten en conocimiento implícito. La iteración rápida con IA amplifica el problema porque cada patch local plausible puede añadir otra ruta oculta.

La decisión inicial de V3 rechazó operar in situ sobre unas 83.000 líneas. El sistema anterior se conservó como evidencia de comportamiento y las capacidades se migraron función por función hacia contracts explícitos.

| Entropía de V2 | Respuesta de Super Dolphin |
|---|---|
| RPC manual y lógica transversal dispersa | typed requests, contract unificado, middleware y errores explícitos |
| Lifecycle y efectos repartidos | state transitions declarativas, eventos tipados y runners con owner |
| Grafo manual de `New()` / `Close()` | composición `fx` y ownership explícito de inicio y cierre |
| Módulos de negocio acoplados a storage/adapters | onion boundaries, Module-owned Ports y anti-corruption adapters |
| Funciones gigantes con abstracciones mezcladas | composed methods y presupuesto `80 / 4 / 10` de longitud, anidamiento y complejidad |
| Convenciones recordadas por reviewers | guards AST/SSA, maps, manifests, hooks y evidencia reproducible |

V2 no es una historia que deba ocultarse, sino el modelo de fallo contra el que se diseñó la gobernanza de Super Dolphin.

### Anticorrupción de ingeniería y AI Code Rot

La IA puede generar código con rapidez; sin límites, también amplifica rápidamente la deriva arquitectónica. Super Dolphin trata esa deriva como **AI Code Rot** y la convierte en fallos visibles para la máquina cerca del punto donde se introduce.

| Capa anticorrupción | Qué evita | Evidencia en el repositorio |
|---|---|---|
| Verdad de navegación | Editar el subsistema incorrecto o usar modelos mentales obsoletos | `docs/doc/codemap`, project map, capability manifest |
| Límites de arquitectura | Dependencias directas de Module hacia Store, Provider, UI o Command | registro tipado de límites y evaluación AST de imports |
| Guards semánticos | Errores ignorados, silent fallback, lifecycle inseguro y seams demasiado amplios | guards AST y análisis priority SSA |
| Presupuesto de complejidad | Funciones gigantes que mezclan negocio, infraestructura, protocolo y persistencia | longitud efectiva `<= 80`, anidamiento `<= 4`, complejidad ciclomática `<= 10` |
| Trinquete de deuda | Empeorar deuda conocida u ocultarla recreando un baseline | particiones production/test freeze rechazan regresiones y se reducen al mejorar |
| Gates reproducibles | Declarar éxito sin mapas, pruebas, artefactos o evidencia exacta | pre-commit, pre-push y AI maintenance gates sensibles al cambio |

El límite de 80 líneas no es un dogma para todos los sistemas. Refleja la carga de orquestación de este repositorio, donde los composed methods con un solo nivel de abstracción son más seguros que procedimientos monolíticos. La regla profunda es: **hacer visible la política, ocultar el detalle detrás de interfaces estrechas y mantener cada excepción explícita y medible.**

### Por qué no es otro Agent Framework

| Agent Framework habitual | Super Dolphin Agent |
|---|---|
| Optimiza la ejecución de tareas | Gobierna cómo las tareas cambian un sistema real |
| Ofrece más herramientas y contexto | Ofrece contexto acotado, capability contracts y direcciones de dependencia permitidas |
| Considera que terminar un run es éxito | Exige tests, diagnostics, estado generado y evidencia Git |
| Depende principalmente de prompts | Aplica invariantes en code, tests, hooks y manifests |
| Oculta fallos con retries o defaults | Falla rápido ante configuración, estado o dependencias inválidas |

```text
intent
  -> code map + capability contract
  -> cambio de IA acotado mediante LSP/MCP
  -> AST/SSA/architecture guards
  -> focused tests + generated artifact checks
  -> reviewable evidence
  -> accepted commit
```

<!-- sd:architecture -->
## Arquitectura

```text
cmd/                 entradas de escritorio, orquestación MCP y LSP sidecar multilenguaje
frontend-app/        interfaz de escritorio React/Vite actual
internal/contract/   interfaces entre módulos y DTO
internal/module/     lógica de Turn, Prompt, Cron, Memory y Skill
internal/platform/   DB, RPC, configuración y seguridad de runtime
internal/provider/   adapters para Codex, Claude CLI y otros Providers
internal/store/      adapters de persistencia basados en sqlc y wrappers manuales
pkg/                 bibliotecas públicas reutilizables
```

La lógica de negocio solo depende de contracts internos. Store actúa como anti-corruption layer entre el dominio y SQL; Provider, MCP y UI permanecen en la capa exterior; `cmd/*` y los composition roots realizan el ensamblado explícito. Consulta el [code map](docs/doc/codemap/README.md) y el [contrato de arquitectura onion](docs/%E5%A5%91%E7%BA%A6/onion-architecture-convention.md).

## Capacidades principales

- Sesiones multiagente, resume, fork, scheduling y eventos en tiempo real.
- Sidecar de orquestación MCP y peer LSP multilenguaje genérico.
- Cron, Memory, Prompt, Thread y skills nativas del Provider.
- Adapters para Codex y Claude CLI protegidos por un contract unificado.
- Persistencia SQLite, host de escritorio Wails y UI React/Vite.
- Code map, project map, capability contract, Archtest y AI maintenance gates.

<!-- sd:quick-start -->
## Inicio rápido

Requisitos: Go 1.25.7, Node.js 20+, OpenAI Codex CLI autenticado, `gopls`, `typescript-language-server` y `typescript@5.9.3`. Claude Code CLI solo es necesario al usar el Provider de Claude.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
( cd frontend-app && npm install )
./run-new-ui-desktop.sh
```

Windows PowerShell:

```powershell
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
cd frontend-app; npm install; cd ..
.\run-new-ui-desktop.ps1
```

SQLite usa `SUPER_DOLPHIN_HOME/super-dolphin.db` de forma predeterminada. `SUPER_DOLPHIN_SQLITE_PATH` permite elegir otro archivo local. Las skills canónicas viven en `<workspace>/.agents/skills/` y `~/.super-dolphin/skills/personal/{user,agent,imported}/`.

<!-- sd:governance-demo -->
## Prueba reproducible de gobernanza

Consulta los gates seleccionados para un cambio:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

Ejecuta las superficies anticorrupción principales:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

Verificación completa:

```bash
make test
( cd frontend-app && npm run lint && npm test && npm run build )
```

Los checks son de solo lectura y fallan cuando la verdad generada está obsoleta. Usa los targets `*-refresh` únicamente cuando quieras actualizar de forma explícita la fuente generada.

<!-- sd:security -->
## Seguridad

- No hagas commit de credenciales, Provider homes, bases de datos locales, logs, user memory ni configuración de la máquina.
- La configuración ausente y las dependencias rotas siguen el [contrato fail-fast](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md); silent fallback es un defecto.
- El exportador de código público solo lee Git objects confirmados y aplica una política default-deny que excluye planes internos, archives, run evidence, workspaces locales y archivos untracked.
- Informa las vulnerabilidades sensibles en privado al propietario del repositorio; no publiques exploits, secrets ni datos de usuario en un Issue público.

<!-- sd:community -->
## Comunidad y contribuciones

Se aceptan Issues y Pull Requests bien enfocados. Mantén los cambios pequeños y verificables, respeta los límites entre módulos, añade regression tests en el mismo commit para los fixes y ejecuta los gates correspondientes. Las decisiones de arquitectura deben vivir en contracts y guards ejecutables, no solo en prompts.

- [Code map](docs/doc/codemap/README.md)
- [Contratos de arquitectura](docs/%E5%A5%91%E7%BA%A6/README.md)
- [Instrucciones para Agents](AGENTS.md)
- [Apache License 2.0](LICENSE)

## Licencia

Distribuido bajo la [Apache License 2.0](LICENSE). Consulta [NOTICE](NOTICE) para la atribución de copyright.
