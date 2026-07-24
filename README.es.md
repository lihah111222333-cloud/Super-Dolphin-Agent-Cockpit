# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**El repositorio autoprotegido para software escrito por IA.** Los agentes de IA implementan los cambios; los maps, contracts, tests y gates que pertenecen al repositorio deciden si esos cambios son lo bastante seguros para conservarse.

> [!IMPORTANT]
> **Declaración de los mantenedores: el código original y la documentación propia del proyecto están escritos al 100% por IA, dirigidos por personas y protegidos por el repositorio.** El código de producto, el código de tests y la documentación propia del proyecto son escritos o refactorizados por agentes de IA. Las personas conservan la responsabilidad sobre la intención del producto, las decisiones de arquitectura, las credenciales y las publicaciones. Que la autoría sea de IA no implica infalibilidad: todo cambio aceptado sigue sujeto a la evidencia y los gates que pertenecen al repositorio. Los textos legales y comunitarios procedentes de terceros conservan su atribución original.

**Aplicación local-first de la entrega.** La aceptación cotidiana de commits y pushes es impuesta por [Git hooks](.githooks/README.md) versionados, sin depender de CI alojada y de pago en GitHub. `pre-commit` comprueba el snapshot en staging, las reglas de mantenimiento por IA, el guard completo del repositorio y el código afectado; `commit-msg` exige evidencia de regresión en los commits de corrección; `pre-push` valida el rango del `HEAD`, ejecuta los checks afectados, analiza nilness en los paquetes Go afectados y corre Race tests para las superficies concurrentes registradas. Los E2E diferidos de Providers, `gosec`/seguridad y release siguen siendo gates separados.

Super Dolphin Agent es un **sistema de ingeniería de vibe coding nativo de IA y de nivel productivo, además de un plano de control para desarrollo multiagente**. Reúne un runtime de escritorio local, orquestación MCP, navegación LSP multilenguaje, integraciones con Providers, workflows persistentes y límites de ingeniería aplicados por máquina en una implementación de referencia funcional.

El [README.md](README.md) en inglés es la descripción normativa. Las traducciones conservan el mismo alcance del producto, comandos, rutas, variables de entorno, identidad del repositorio y licencia. Los detalles se encuentran en [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md) y el [Code Map](docs/doc/codemap/README.md) generado.

<!-- sd:why -->
## Por qué existe Super Dolphin

La mayoría de los Agent frameworks optimizan la ejecución de tareas. Super Dolphin también gobierna qué puede modificar una tarea completada dentro de un sistema de software de larga duración.

Su ciclo de mantenimiento tiene seis etapas:

1. **Orientar** mediante code maps generados y capability contracts.
2. **Comprender** definiciones, referencias, jerarquías de llamadas y diagnósticos mediante LSP.
3. **Cambiar** una superficie acotada y con ownership explícito.
4. **Restringir** el diff mediante reglas AST/SSA, límites de dependencias, presupuestos de complejidad y contratos fail-fast.
5. **Demostrar** el resultado mediante tests focalizados, comprobaciones de artefactos generados y gates sensibles al cambio.

6. **Aprender** de correcciones demostradas: extraer la causa raíz, generalizar el invariante y promover patrones recurrentes a evidencia de regresión o guards ejecutables.

### Guardrails para el vibe coding

La IA puede generar código decenas o cientos de veces más rápido que una persona, por lo que el cuello de botella pasa de producir código a probarlo y entregarlo con confianza. Corregir una instancia no es terminar si el mismo patrón de defecto puede seguir presente en otros lugares o reaparecer en código generado por IA.

Super Dolphin consolida periódicamente evidencia de bug fixes demostrados por tests o por el uso real como conocimiento de ingeniería reutilizable. Los patrones estables se promueven a tests, fixtures, reglas AST/SSA, políticas de dependencias u otros gates ejecutables propiedad del repositorio. Si un agente de IA reproduce un bad smell conocido, el gate rechaza el cambio y obliga a repararlo antes de la entrega.

Los Skills y prompts pueden guiar la generación; los guards imponen qué puede aceptarse. Todo guard candidato necesita evidencia reproducible, un invariante generalizable y comprobaciones de aceptación deterministas: es un ratchet basado en evidencia, no una automodificación ciega. El repositorio ya implementa consolidación automática de memoria y una amplia infraestructura de guards; la promoción end-to-end totalmente automática de cada corrección a un nuevo guard ejecutable sigue siendo una dirección de ingeniería, no una afirmación de cobertura completa.

Esta es la dirección del vibe coding nativo de IA: las personas definen la intención, la arquitectura y los límites de aceptación; la IA genera únicamente dentro de esas especificaciones; el repositorio aprende de los defectos y se vuelve progresivamente más robusto y legible, sin depender de que las personas redescubran la misma clase de bug.

### Vibe coding de nivel productivo, no solo autonomía del agente

Proyectos reconocidos como [Hermes Agent](https://github.com/NousResearch/hermes-agent) y [OpenClaw](https://github.com/openclaw/openclaw) demuestran el valor de la ejecución autónoma, el uso amplio de herramientas, la memoria persistente y los Skills reutilizables. Hermes prioriza un learning loop que crea y mejora Skills a partir de la experiencia; OpenClaw prioriza un asistente personal de IA que actúa entre sistemas operativos, plataformas de mensajería y servicios.

Las capacidades de un sistema de agentes no son fijas. Este proyecto fue iniciado por una sola persona y está escrito principalmente por IA, por lo que el tiempo, la experiencia y los casos de uso de un único mantenedor son limitados y necesita la colaboración de la comunidad. Los contribuidores pueden enviar PRs con módulos, integraciones, UI, Skills, MCP, Providers y herramientas, o aportar escenarios objetivo, especificaciones, tests de aceptación y defectos reales sin escribir la implementación. El sistema aplica las mismas restricciones fuertes al código comunitario y al generado por IA, y convierte necesidades pendientes en tareas que obligan a la IA a generar o reparar módulos completos conformes con la arquitectura, los contracts y los gates. Así combina la colaboración comunitaria con la velocidad de la IA para alcanzar o superar rápidamente a Hermes Agent y OpenClaw sin depender de que un mantenedor programe cada capacidad.

Hermes Agent y OpenClaw muestran hasta dónde pueden llegar la ejecución autónoma, la integración amplia de herramientas y la iteración rápida. También aclaran un desafío común: a medida que crecen las funciones, los canales y los entornos de ejecución, los tests y guards locales por sí solos no pueden mantener un repositorio comprensible y evolutivo. Módulos centrales sobredimensionados, responsabilidades dispersas y rutas de impacto difíciles de seguir pueden dejar funcionando cada función mientras el coste de mantener todo el sistema sigue creciendo.

Ese es el problema que Super Dolphin está diseñado para resolver. Tanto si el código lo produce solo la IA como si la IA lo produce junto con ingenieros humanos de élite, la velocidad sostenible exige especificaciones, contracts, tests de regresión y gates ejecutables impuestos por el repositorio para mantener alineados arquitectura, comportamiento demostrado e intención del producto.

Aunque el código se produzca decenas o cientos de veces más rápido, la capacidad de revisión humana no puede crecer en la misma proporción. Sin especificaciones, contracts, tests de regresión y gates ejecutables impuestos por el repositorio, incluso un equipo profesional pierde gradualmente el control de su código. Las funciones locales pueden seguir operativas mientras las rutas duplicadas, la ambigüedad del lifecycle, el acoplamiento oculto y los supuestos no comprobados se acumulan hasta volver el sistema cada vez más difícil de comprender, probar, entregar y mantener.

La ventaja de Super Dolphin es la **iteración sostenible**. Trata el propio repositorio como sistema de control para absorber nuevas capacidades aportadas por la comunidad sin convertir rápidamente la base de código en una arquitectura imposible de mantener. Las especificaciones definen la intención; los contracts tipados y los límites de dependencias restringen la implementación; los tests y fixtures de regresión preservan el comportamiento demostrado; los guards AST/SSA y gates sensibles al cambio rechazan bad smells conocidos. Las funciones pueden seguir creciendo, pero solo se acepta el código que satisface la especificación ejecutable del repositorio.

### Ruta para igualar y superar a los agentes líderes

Con la generación rápida de código por IA, el código de capacidad no es el recurso escaso. Lo escaso es un sistema de restricciones que transforme de forma fiable las necesidades y contribuciones de la comunidad en módulos conformes. Super Dolphin sigue esta ruta:

1. **La comunidad define el escenario real que se debe alcanzar.** Aporta workflows, resultados esperados y casos de fallo que Hermes Agent u OpenClaw ya resuelven, o todavía no.
2. **Convertir el escenario en una especificación ejecutable.** Antes de generar o enviar código se definen ownership del módulo, contracts tipados, dirección de dependencias, límites de seguridad, tests de aceptación y evidencia de entrega.
3. **Implementar por dos vías: PRs comunitarios y generación por IA.** Los contribuidores pueden enviar módulos completos o integraciones acotadas; la IA puede usar code maps y LSP para implementar backend, integraciones, UI necesaria, tests y documentación. Ninguna vía puede saltarse la arquitectura.
4. **Aplicar los mismos gates duros a todo el código.** Build, tests, escenarios E2E, permisos y lifecycle, guards AST/SSA, límites de dependencias y gates sensibles al cambio evalúan por igual el código comunitario y el generado por IA; lo no conforme debe repararlo el contribuidor o la IA.
5. **Demostrar paridad en tareas reales.** “Respondió una vez” no es paridad. Solo se valida una capacidad cuando completa de forma reproducible el workflow objetivo y sus rutas de fallo.
6. **Convertir el uso comunitario en la siguiente restricción de generación.** Fallos de producción, regresiones y correcciones repetidas se convierten en fixtures, especificaciones y guards ejecutables para que módulos posteriores eviten defectos conocidos.

Esta no es una ruta para que un mantenedor programe todas las funciones. La comunidad puede aportar código, problemas y evidencia; el sistema restringe el código comunitario y hace que la IA complete o repare módulos. Así amplifica una capacidad de mantenimiento limitada mediante colaboración comunitaria y producción de ingeniería de IA para alcanzar o superar rápidamente a Hermes Agent y OpenClaw sin perder la mantenibilidad.

### Mantenimiento con contexto acotado

La naturaleza de este proyecto no exige leer todo el código fuente. Ni los ingenieros ni la IA necesitan comprender el repositorio completo antes de empezar. Se parte de la capacidad objetivo y del resultado de aceptación, se utilizan code maps, ownership de módulos, contracts estrechos y gates deterministas para obtener únicamente el contexto necesario, y se ejecuta dentro de esas restricciones corrigiendo las infracciones hasta entregar la función deseada.

Esto no garantiza que todos los cambios sean locales. El trabajo transversal sigue exigiendo ampliar el análisis de referencias e impacto, pero ampliar el contexto necesario no equivale a leer todo el repositorio; todo cambio aceptado continúa sujeto a los tests y la evidencia de revisión correspondientes.

Según el criterio arquitectónico de este proyecto, cualquier implementación que obligue a leer todo el código para descubrir el impacto de un cambio no es adecuada para el mantenimiento continuo por IA y tampoco es mantenible por ingenieros que no posean un conocimiento total del sistema. Normalmente indica que el ownership de módulos, la dirección de dependencias, los contracts o los guards no hicieron explícita la superficie de impacto. Las restricciones deben convertir dependencias y consecuencias en señales de máquina navegables, verificables y fail-fast, sin depender de que un experto recuerde todo el sistema.

### Mantenimiento AI-first y semántica bajo responsabilidad del ingeniero

El repositorio está organizado deliberadamente para que la IA pueda localizar, comprender, modificar y verificar código bajo restricciones. Los contracts explícitos, módulos estrechos, funciones pequeñas, maps generados y límites legibles por máquina pueden parecer más verbosos o fragmentados al leer los archivos linealmente. La comodidad de lectura humana no es, por tanto, el único objetivo de optimización. Esto no autoriza duplicación ni código ambiguo: cada límite adicional debe mejorar la navegación, el aislamiento del impacto o la verificación determinista.

Las funciones pequeñas no se consideran un problema solo porque aumenten el número de símbolos. La IA comprende el sistema mediante definiciones, referencias, jerarquías de llamadas, code maps y tests, no leyendo un archivo largo como narración. Se recomienda a los contribuidores leer el repositorio junto con un asistente de IA y sus herramientas de navegación, en vez de construir un modelo mental completo únicamente desde el código fuente.

Por tanto, el repositorio no debe evaluarse únicamente con el modo tradicional de leer manualmente archivo tras archivo y seguir cada cadena de llamadas. Una evaluación más pertinente consiste en hacer que la IA utilice code maps, LSP, contracts, tests y gates para completar un ciclo real de localizar, comprender, analizar impacto, modificar y verificar, y solo entonces valorar la facilidad de lectura, modificación y mantenimiento.

La IA puede implementar un diseño especificado, pero no puede apropiarse ni decidir por sí sola la semántica de negocio: qué problema resolver, qué significa una función, cómo deben comportarse los módulos, cuál debe ser el resultado visible final y qué compromisos son aceptables. Son decisiones de dirección, no tareas de generación de código.

Las personas toman las decisiones y conservan el volante: deciden qué problema resolver, la semántica de negocio, el resultado visible esperado y qué compromisos son aceptables. Después de que la IA escriba el código, las personas todavía deben comprobar que la función satisface de verdad la necesidad prevista. Esa responsabilidad no puede delegarse en un agente.

Una producción de código más rápida no hace que los tests sean gratuitos ni exponencialmente más baratos. Genera más cambios, combinaciones y superficie de riesgo de negocio que validar; cuanto más rápido se escribe, mayor es la carga de test y aceptación. Esta arquitectura ya sitúa aproximadamente el 90% de los problemas detectables o prevenibles por máquina detrás de contracts, guards estáticos, tests y gates; las personas verifican el 10% restante: que el requisito se expresó correctamente, que el comportamiento es correcto en el uso real y que el producto sigue en la dirección adecuada.

Por ello, los ingenieros siguen ocupando el centro del proyecto. Definen la dirección del producto, la semántica de negocio, las responsabilidades de los módulos, la arquitectura, los criterios de aceptación y los límites de riesgo; la IA convierte esas decisiones en código, tests, documentación y mantenimiento repetible bajo los gates del repositorio. El objetivo no es eliminar a los ingenieros, sino trasladar su atención de leer y escribir cada función pequeña a gobernar el significado, la evidencia y la evolución del sistema. **Los ingenieros conservan el volante; la IA aumenta el rendimiento de ingeniería, pero no sustituye el juicio sobre la dirección de negocio ni los resultados de entrega.**

### Evaluación independiente de mantenibilidad por IA

El 13 de julio de 2026, tres agentes independientes que utilizaron el modelo GPT-5.6 con razonamiento medio calificaron conjuntamente la capacidad pura de mantenimiento de código por IA de este repositorio con **95/100 (A+)**; otro subagente, al que se le prohibió leer la puntuación existente, reprodujo **95.6/100 (A+)** mediante una evaluación ciega. Los code maps, la navegación LSP, los contracts estrechos, los architecture guards y el ciclo de verificación determinista permiten que la IA localice, analice, implemente y verifique cambios sin leer todo el código, mientras las personas conservan la dirección del producto, la semántica de negocio, las decisiones de aceptación y la documentación.

**Prompt de reproducción:** Evalúa este repositorio sobre 100 desde la perspectiva del mantenimiento puro por IA, suponiendo que las personas solo controlan dirección, semántica de negocio, aceptación y documentación mientras la IA ejecuta los documentos de diseño y se encarga de localizar código, analizar impacto, implementar, probar, diagnosticar y entregar, ignorando el coste de tokens, sin evaluar UI, release ni madurez comercial, sin leer la puntuación existente del README y mostrando evidencia actual, puntuaciones por categoría y por qué no alcanza 100.

### Evolución del desarrollo: por qué existe Super Dolphin

Super Dolphin es la tercera gran etapa de una evolución de ingeniería continua. Desde la primera etapa V1, pasando por el predecesor directo `go-agent-v2` (V2), hasta el V3 actual, esta evolución no consiste solo en añadir funciones de agente. Resuelve iterativamente un mismo problema de ingeniería: cada cambio debe exponer su impacto dentro de un contexto acotado, ejecutarse bajo restricciones explícitas y verificarse por máquina sin exigir que la IA o las personas lean y recuerden todo el código.

1. **La primera etapa** fue una herramienta multiagente de línea de comandos escrita en Python. Validó que los modelos podían dividir tareas, colaborar mediante herramientas y completar trabajo de ingeniería real.
2. **`go-agent-v2` fue el predecesor directo de este proyecto.** Evolucionó desde un sistema interno de distribución de tareas hasta un sistema de ingeniería operativo que reunía workflows automatizados de trading cuantitativo, controles de escritorio multiagente, integración con Providers y ejecución persistente. Demostró el valor de la dirección del producto en trabajo real; no era un prototipo desechable.
3. **Super Dolphin / V3 comenzó el 19 de marzo de 2026** como una nueva generación arquitectónica. Conserva las capacidades y lecciones operativas del predecesor, mientras reconstruye las bases necesarias para un desarrollo de largo plazo impulsado por IA.

V3 no nació porque el predecesor no funcionara. Funcionaba y seguía acumulando funciones, pero la IA podía generar cambios locales más rápido de lo que una arquitectura basada en convenciones y revisión humana podía absorber con seguridad. Los tests podían demostrar una ruta individual mientras el ownership, el lifecycle, la dirección de dependencias y la legibilidad seguían degradándose en el conjunto del sistema. Según los registros de los mantenedores anteriores a la publicación, esa presión tomó formas concretas:

- más de 80 métodos RPC acumularon rutas paralelas de binding, validation, capability y logging;
- el ownership del lifecycle se dispersó entre managers y efectos secundarios asíncronos;
- un event handler central alcanzó 557 líneas;
- el ensamblado manual de la aplicación superó las 200 líneas.

Por eso V3 es más que una actualización funcional. Traslada el conocimiento arquitectónico desde la memoria de los reviewers y los prompts hacia contracts, code maps, límites tipados, evidencia de regresión y gates ejecutables propiedad del repositorio. El modo de fallo que combate es el **AI code rot**: los cambios locales siguen funcionando mientras se degradan los contracts globales, los límites de ownership y la legibilidad.

El historial privado del predecesor es contexto aportado por los mantenedores, no evidencia pública. Por ello, el repositorio público expone las respuestas arquitectónicas, los guards, los fixtures de regresión y los comandos reproducibles creados a partir de esas lecciones.

| Presión observada en el predecesor | Respuesta de Super Dolphin |
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

### Integridad de los datos durante el transporte y el cálculo

Los datos no se consideran confiables solo por haber atravesado un límite tipado. Cada límite valida el invariante que le pertenece: los binders RPC rechazan valores de transporte mal formados; los DTO tipados y los ports estrechos restringen la forma y el ownership; los services validan reglas de negocio y normalizan la entrada antes del cálculo; los guards de campos de los mappers detectan campos omitidos u obsoletos; y sqlc junto con las restricciones de SQLite protegen la persistencia. Los workflows de larga duración añaden claves de idempotencia, leases, claim tokens y transiciones de estado CAS para impedir que workers obsoletos sobrescriban la ejecución actual.

Los cálculos pueden fallar explícitamente incluso después de una validación previa. La lógica de scheduling, identidad, configuración, reintentos y transición de estados devuelve errores en lugar de sustituir datos silenciosamente. El flujo de Cron es un ejemplo concreto: los parámetros JSON-RPC se convierten en una request tipada, la validación del service precede al cálculo del schedule, sqlc persiste registros restringidos, el scheduler reclama atómicamente el trabajo vencido y los resultados del turn solo se confirman cuando el run, el claim token y el estado esperado siguen coincidiendo. Los tests y guards cubren estos límites declarados, pero son evidencia acotada, no una afirmación de que todo futuro campo de negocio quede demostrado automáticamente de extremo a extremo; cada nuevo campo entre capas debe ampliar el mapper, contract, schema y la evidencia de regresión correspondientes.

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
- Node.js compatible con `^20.19.0 || ^22.13.0 || >=24` y npm
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
