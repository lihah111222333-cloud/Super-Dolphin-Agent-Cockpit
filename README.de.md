# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**Das selbstschützende Repository für KI-geschriebene Software.** KI-Agenten implementieren Änderungen; repository-eigene Maps, Contracts, Tests und Gates entscheiden, ob diese Änderungen sicher genug sind, um erhalten zu bleiben.

> [!IMPORTANT]
> **Erklärung der Maintainer: Originalcode und projekteigene Dokumentation sind zu 100% KI-geschrieben, von Menschen gesteuert und durch das Repository geschützt.** Produktcode, Testcode und projekteigene Dokumentation werden von KI-Agenten geschrieben oder refaktoriert. Menschen bleiben für Produktabsicht, Architekturentscheidungen, Zugangsdaten und Veröffentlichungen verantwortlich. KI-Autorschaft bedeutet keine Unfehlbarkeit: Jede akzeptierte Änderung unterliegt weiterhin den repository-eigenen Nachweisen und Gates. Rechtliche und Community-Texte aus Upstream-Quellen behalten ihre ursprüngliche Namensnennung.

Super Dolphin Agent ist ein **produktionstaugliches, KI-natives Vibe-Coding-Engineering-System und eine Kontrollplattform für Multi-Agenten-Entwicklung**. Es verbindet eine lokale Desktop-Runtime, MCP-Orchestrierung, mehrsprachige LSP-Navigation, Provider-Integrationen, persistente Workflows und maschinell erzwungene Engineering-Grenzen in einer funktionsfähigen Referenzimplementierung.

Die englische [README.md](README.md) ist die maßgebliche Übersicht. Die Übersetzungen bewahren denselben Produktumfang, dieselben Befehle, Pfade, Umgebungsvariablen, dieselbe Repository-Identität und Lizenz. Ausführliche Fakten stehen in [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md) und der generierten [Code Map](docs/doc/codemap/README.md).

<!-- sd:why -->
## Warum Super Dolphin existiert

Die meisten Agent Frameworks optimieren die Ausführung von Aufgaben. Super Dolphin steuert zusätzlich, was eine abgeschlossene Aufgabe in einem langlebigen Softwaresystem verändern darf.

Der Wartungszyklus besteht aus sechs Phasen:

1. **Orientieren** mit generierten Code Maps und Capability Contracts.
2. **Verstehen** von Definitionen, Referenzen, Aufrufhierarchien und Diagnosen über LSP.
3. **Ändern** einer eng begrenzten Fläche mit eindeutigem Ownership.
4. **Begrenzen** des Diffs mit AST/SSA-Regeln, Abhängigkeitsgrenzen, Komplexitätsbudgets und Fail-Fast-Contracts.
5. **Nachweisen** des Ergebnisses mit fokussierten Tests, Prüfungen generierter Artefakte und änderungssensitiven Gates.

6. **Lernen** aus nachgewiesenen Fehlerbehebungen: Ursache ableiten, Invariante verallgemeinern und wiederkehrende Muster zu Regressionsevidenz oder ausführbaren Guards hochstufen.

### Guardrails für Vibe Coding

KI kann Code zehn- bis hundertmal schneller erzeugen als ein Mensch ihn schreibt. Dadurch verschiebt sich der Engpass von der Codeproduktion zu Tests und vertrauenswürdiger Auslieferung. Eine einzelne Fundstelle zu beheben ist nicht abgeschlossen, wenn dasselbe Fehlermuster andernorts weiterbestehen oder in KI-generiertem Code zurückkehren kann.

Super Dolphin konsolidiert regelmäßig Bugfix-Evidenz, die durch Tests oder reale Nutzung nachgewiesen wurde, zu wiederverwendbarem Engineering-Wissen. Stabile Muster werden zu repository-eigenen Tests, Fixtures, AST/SSA-Regeln, Dependency Policies oder anderen ausführbaren Gates hochgestuft. Erzeugt ein KI-Agent einen bekannten Bad Smell erneut, lehnt das Gate die Änderung ab und erzwingt eine Korrektur vor der Auslieferung.

Skills und Prompts können die Generierung anleiten; Guards erzwingen, was akzeptiert werden darf. Ein Guard-Kandidat benötigt reproduzierbare Evidenz, eine verallgemeinerbare Invariante und deterministische Abnahmeprüfungen. Das ist ein evidenzgetriebener Ratchet, keine unkontrollierte Selbstmodifikation. Das Repository implementiert bereits automatische Memory Consolidation und eine umfangreiche Guard-Infrastruktur; die vollständig automatische End-to-End-Hochstufung jeder Fehlerbehebung zu einem neuen ausführbaren Guard bleibt eine Engineering-Richtung und ist keine Behauptung vollständiger Abdeckung.

Das ist die Richtung von KI-nativem Vibe Coding: Menschen definieren Absicht, Architektur und Akzeptanzgrenzen; KI erzeugt Code nur innerhalb dieser Spezifikationen; das Repository lernt aus Defekten und wird schrittweise robuster und verständlicher, ohne darauf angewiesen zu sein, dass Menschen dieselbe Fehlerklasse immer wieder neu entdecken.

### Produktionstaugliches Vibe Coding, nicht nur Agentenautonomie

Bekannte Projekte wie [Hermes Agent](https://github.com/NousResearch/hermes-agent) und [OpenClaw](https://github.com/openclaw/openclaw) zeigen den Wert autonomer Ausführung, breiter Werkzeugnutzung, persistenten Speichers und wiederverwendbarer Skills. Hermes betont einen Learning Loop, der aus Erfahrung Skills erstellt und verbessert; OpenClaw betont einen persönlichen KI-Assistenten, der über Betriebssysteme, Messaging-Plattformen und Dienste hinweg handelt.

Die Fähigkeiten eines Agentensystems sind nicht festgeschrieben. Dieses Projekt wurde von einer Person gestartet und wird überwiegend von KI geschrieben; Zeit, Erfahrung und Anwendungsfälle eines einzelnen Maintainers sind daher begrenzt, und das Projekt braucht die Community. Beitragende können PRs mit Modulen, Integrationen, UI, Skills, MCP, Providern und Werkzeugen einreichen oder ohne eigene Implementierung Zielszenarien, Spezifikationen, Akzeptanztests und reale Defekte beitragen. Das Engineering-System wendet dieselben harten Vorgaben auf Community-Code und KI-generierten Code an und verwandelt offene Anforderungen in Aufgaben, die KI zur Erzeugung oder Korrektur vollständiger, architektur-, contract- und gate-konformer Module zwingen. So werden Community-Zusammenarbeit und KI-Generierung gemeinsam verstärkt, um Hermes Agent und OpenClaw schnell zu erreichen oder zu übertreffen, ohne dass ein Maintainer jede Fähigkeit von Hand programmiert.

Diese bekannten Projekte zeigen zugleich eine inhärente Grenze von Agent-first Vibe Coding: Mehr Autonomie, mehr Werkzeuge, persistenter Speicher und selbstverbessernde Skills machen die Entwicklung eines Repositorys nicht von selbst kontrollierbar. Wenn KI-Agenten eine große Codebasis fortlaufend verändern, bleiben die entscheidenden Fragen offen: Wer bewahrt die Architektur, testet die richtigen Eigenschaften, verhindert die Rückkehr eines nachweislich schlechten Musters und entscheidet, welcher Code bestehen darf?

Auch wenn Code zehn- bis hundertmal schneller erzeugt wird, kann die menschliche Review-Kapazität nicht im gleichen Verhältnis wachsen. Ohne vom Repository erzwungene Spezifikationen, Contracts, Regressionstests und ausführbare Gates verliert selbst ein professionelles Engineering-Team schrittweise die Kontrolle über seinen Code. Lokale Funktionen können weiterarbeiten, während doppelte Pfade, unklare Lifecycles, versteckte Kopplung und ungeprüfte Annahmen sich ansammeln und das System immer schwerer verständlich, testbar, auslieferbar und wartbar machen.

Der Vorteil von Super Dolphin ist **nachhaltige Iteration**. Es behandelt das Repository selbst als Kontrollsystem, damit neue Community-Fähigkeiten aufgenommen werden können, ohne die Codebasis rasch in eine unwartbare Architektur zu verwandeln. Spezifikationen definieren die Absicht; typisierte Contracts und Abhängigkeitsgrenzen beschränken die Implementierung; Tests und Regression Fixtures bewahren nachgewiesenes Verhalten; AST/SSA-Guards und änderungssensitive Gates lehnen bekannte Bad Smells ab. Funktionen dürfen weiter wachsen, aber nur Code, der die ausführbare Spezifikation des Repositorys erfüllt, wird akzeptiert.

### Route zum Erreichen und Übertreffen führender Agenten

Bei schneller KI-Codegenerierung ist Capability-Code nicht die knappe Ressource. Knapp ist ein System harter Engineering-Vorgaben, das Community-Anforderungen und Beiträge zuverlässig in konforme Module verwandelt. Super Dolphin folgt diesem Weg:

1. **Die Community definiert das reale Zielszenario.** Sie liefert Workflows, erwartete Ergebnisse und Fehlerfälle, die Hermes Agent oder OpenClaw bereits lösen oder noch nicht lösen.
2. **Das Szenario wird zur ausführbaren Spezifikation.** Vor Generierung oder Einreichung von Code werden Module Ownership, typisierte Contracts, Abhängigkeitsrichtung, Sicherheitsgrenzen, Akzeptanztests und Auslieferungsnachweise festgelegt.
3. **Implementierung über zwei Wege: Community-PRs und KI-Generierung.** Beitragende können vollständige Module oder begrenzte Integrationen einreichen; KI kann mit Code Maps und LSP Backend, Integrationen, notwendige UI, Tests und Dokumentation implementieren. Keiner der Wege darf die Architektur umgehen.
4. **Dieselben harten Gates gelten für jeden Code.** Build, Tests, E2E-Szenarien, Berechtigungs- und Lifecycle-Prüfungen, AST/SSA-Guards, Abhängigkeitsgrenzen und änderungssensitive Gates prüfen Community- und KI-Code gleich; Nichtkonformes muss durch Beitragende oder KI korrigiert werden.
5. **Parität wird an realen Aufgaben bewiesen.** „Einmal geantwortet“ ist keine Parität. Eine Fähigkeit ist erst validiert, wenn sie Ziel-Workflow und Fehlerpfade reproduzierbar abschließt.
6. **Community-Nutzung wird zur nächsten Generierungsvorgabe.** Produktionsfehler, Regressionen und wiederholte Korrekturen werden zu Fixtures, Spezifikationen und ausführbaren Guards, sodass spätere Module bekannte Defekte vermeiden.

Dies ist kein Weg, auf dem ein Maintainer alle Funktionen von Hand schreibt. Die Community kann Code, Probleme und Evidenz beitragen; das System begrenzt Community-Code und lässt KI Module vervollständigen oder korrigieren. So wird begrenzte Wartungskapazität durch Community-Zusammenarbeit und KI-Engineering-Durchsatz verstärkt, um Hermes Agent und OpenClaw schnell zu erreichen oder zu übertreffen und zugleich Wartbarkeit zu bewahren.

### Wartung mit begrenztem Kontext

Das Repository ist so ausgelegt, dass routinemäßige Änderungen nicht die gesamte Codebasis in einen einzigen Modellkontext laden müssen. Generierte Navigation, enge Contracts und deterministische Fehler helfen einem Agenten, die relevante Fläche zu finden und Regelverletzungen schnell zu beheben.

Das garantiert nicht, dass jede Änderung lokal bleibt. Querschnittsänderungen erfordern weiterhin eine breitere Referenz- und Auswirkungsanalyse, und jede akzeptierte Änderung benötigt die zugehörigen Tests und Review-Nachweise.

### Entwicklungsgeschichte: Warum Super Dolphin entstand

Super Dolphin ist die dritte große Stufe einer kontinuierlichen Engineering-Entwicklung:

1. **Die erste Stufe** war ein in Python geschriebenes Multi-Agenten-Kommandozeilenwerkzeug. Es bestätigte, dass Modelle Aufgaben aufteilen, über Werkzeuge zusammenarbeiten und echte Engineering-Arbeit erledigen können.
2. **`go-agent-v2` war der direkte Vorgänger dieses Projekts.** Es entwickelte sich von einer internen Aufgabenverteilung zu einem produktiv nutzbaren Engineering-System, das automatisierte quantitative Trading-Workflows, Multi-Agenten-Desktopsteuerung, Provider-Integration und persistente Ausführung verband. Es bewies den Wert der Produktrichtung in realer Arbeit und war kein Wegwerfprototyp.
3. **Super Dolphin / V3 startete am 19. März 2026** als neue Architekturgeneration. Es übernimmt die Fähigkeiten und Betriebserfahrungen des Vorgängers und baut zugleich die Grundlagen für langfristige KI-getriebene Entwicklung neu auf.

V3 entstand nicht, weil der Vorgänger nicht funktionierte. Er funktionierte und erhielt laufend neue Funktionen. KI konnte lokale Änderungen jedoch schneller erzeugen, als eine von Konventionen und menschlichen Reviews abhängige Architektur sie sicher aufnehmen konnte. Tests konnten einen einzelnen Pfad bestätigen, während Ownership, Lifecycle, Abhängigkeitsrichtung und Verständlichkeit im Gesamtsystem weiter verfielen. Laut den Aufzeichnungen der Maintainer vor der Veröffentlichung zeigte sich dieser Druck konkret:

- Mehr als 80 RPC-Methoden sammelten parallele Pfade für Binding, Validierung, Capability-Prüfung und Logging an.
- Lifecycle-Ownership verteilte sich auf mehrere Manager und asynchrone Seiteneffekte.
- Ein zentraler Event Handler wuchs auf 557 Zeilen.
- Die manuelle Anwendungsassemblierung überschritt 200 Zeilen.

Deshalb ist V3 mehr als ein Feature-Upgrade. Architekturwissen wird aus dem Gedächtnis von Reviewern und aus Prompts in repository-eigene Contracts, Code Maps, typisierte Grenzen, Regressionsevidenz und ausführbare Gates verlagert. Der adressierte Fehlermodus ist **AI Code Rot**: Lokale Änderungen funktionieren weiterhin, während globale Contracts, Ownership-Grenzen und Verständlichkeit verfallen.

Die private Entwicklungsgeschichte des Vorgängers ist von den Maintainern bereitgestellter Kontext und kein öffentlicher Nachweis. Das öffentliche Repository zeigt daher die aus diesen Erkenntnissen entstandenen Architekturantworten, Guards, Regression Fixtures und reproduzierbaren Befehle.

| Beim Vorgänger beobachteter Engineering-Druck | Antwort von Super Dolphin |
|---|---|
| Parallele, handgeschriebene RPC-Pfade | Typisierte Requests, eine Contract-Fläche, explizite Middleware und Fehlersemantik |
| Verteilte Lifecycle-Seiteneffekte | Deklarative Transitionen, typisierte Events und Lifecycle Runner mit eindeutigem Ownership |
| Manuelle Objektgraphen | `fx`-Composition mit explizitem Ownership für Start und Beendigung |
| An Adapter gekoppelter Business Code | Onion-Grenzen, Module-owned Ports und Anti-Corruption-Adapter |
| Riesenfunktionen mit vermischten Abstraktionsebenen | Ein repository-spezifisches `80 / 4 / 10`-Budget für Funktionslänge, Verschachtelung und Komplexität |
| Reviewer-Gedächtnis als Policy | AST/SSA-Guards, generierte Maps, Manifeste, Hooks und reproduzierbare Nachweise |

Das `80 / 4 / 10`-Budget ist keine universelle Stilregel. Es ist eine schrittweise verschärfte Vorgabe für dieses orchestrierungsintensive Repository: standardmäßig effektive Funktionslänge `<= 80`, Verschachtelung `<= 4` und zyklomatische Komplexität `<= 10`.

### Was das Repository erzwingt

| Ebene | Schützt vor | Nachweis im Repository |
|---|---|---|
| Navigationswahrheit | Bearbeitung des falschen Subsystems oder Nutzung veralteten Projektwissens | `docs/doc/codemap`, Project Map, Capability Manifest |
| Architekturgrenzen | Zugriffe von Domain Code auf Store-, Provider-, UI- oder Command-Implementierungen | Typisiertes Backend-Boundary-Register und AST-Importauswertung |
| Semantische Guards | Ignorierte Fehler, Silent Fallback, unsichere Lifecycle-Pfade und Weitergabe zu breiter Services | AST-Guards und priorisierte SSA-Analyse |
| Komplexitäts-Ratchets | Zunahme bekannter struktureller Schulden durch neuen Code | Partitionen für Funktion, Verschachtelung, Komplexität sowie Production/Test Freeze |
| Abnahmenachweise | Bewertung des „Done“-Status eines Agenten als Beweis | Fokussierte Tests, Prüfungen generierten Zustands, Git Hooks und änderungssensitive Gates |

### Historisch belegte Fälle

Die Maintainer berichten von fünf Vorfällen vor der Veröffentlichung, für die heute öffentliche Regression-Nachweise existieren: LSP-Scope aus dem falschen Worktree, fehlende Provider Identity, fehlende Runtime Truth für einen persistenten Agenten, still verworfene asynchrone UI-Fehler und ein Type-Alias-Bypass in einem Architektur-Guard.

Lies in [Governance in Action](docs/open-source/GOVERNANCE.md), wie historische Vorfälle von öffentlichen Nachweisen abgegrenzt werden, und führe alle dort erhaltenen Beweise aus.

### Warum dies kein weiteres Agent Framework ist

| Typisches Agent Framework | Super Dolphin Agent |
|---|---|
| Optimiert die Ausführung von Aufgaben | Steuert, wie Aufgaben ein reales Softwaresystem verändern |
| Gibt Agenten mehr Tools und Kontext | Gibt Agenten begrenzten Kontext und erlaubte Abhängigkeitsrichtungen |
| Wertet einen abgeschlossenen Run als Erfolg | Verlangt Tests, Diagnosen, Prüfungen generierten Zustands und Git-Nachweise |
| Verlässt sich hauptsächlich auf Prompt-Disziplin | Erzwingt Invarianten in Code, Tests, Hooks und generierten Manifesten |
| Verdeckt fehlenden Zustand durch Retries oder Defaults | Bricht bei fehlender Konfiguration, Identität, Ownership oder Abhängigkeit sofort ab |

<!-- sd:architecture -->
## Architektur

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

Die zentrale Abhängigkeitsregel ist nach innen gerichtetes Ownership: Module definieren die benötigten Ports, Adapter implementieren diese Ports; Platform- und Provider-Pakete dürfen nicht nach oben in Business-Module importieren. Das Backend-Boundary-Register ist die einzige Quelle, aus der die Architekturregelkarte generiert wird.

[Architecture](docs/open-source/ARCHITECTURE.md) beschreibt Komponentenverantwortung, Datenfluss, Wahrheitsquellen und bekannten Umfang. Für die Navigation auf Dateiebene dient die generierte [Code Map](docs/doc/codemap/README.md).

### Aktueller Umfang

- Die Desktop-Anwendung und ihr repository-spezifischer Governance-Zyklus sind hier implementiert.
- `make guard` und die zugehörigen Prüfungen steuern dieses Repository; sie werden nicht als allgemeiner Scanner für beliebige Repositories beworben.
- Die eingecheckte Public-Source-Policy und die Validierungsbausteine bilden die Grundlage für die Veröffentlichungsreife. Eine vollständige Source-Export-CLI, ein versiegelter Receipt-Workflow, ein öffentliches CI-Gate und eine eigenständige Guard-Distribution sind noch keine veröffentlichten Fähigkeiten.
- Die kanonische GitHub-URL in dieser Dokumentation ist das Veröffentlichungsziel. Clone-, Issue- und Private-Reporting-Links werden erst nutzbar, nachdem der Repository-Eigentümer die Release-Checkliste abgeschlossen hat.
- Codex wird für den aktuellen Desktop-Provider-Ablauf benötigt. Claude wird nur für Arbeiten eingesetzt, die ausdrücklich seine Provider-Integration betreffen.

<!-- sd:quick-start -->
## Schnellstart

### Voraussetzungen

- Go 1.25.7
- Node.js 20+ und npm
- Installierte und authentifizierte OpenAI Codex CLI (`codex`)
- `gopls`
- `typescript-language-server` und TypeScript 5.9.3

Der folgende Clone-Befehl zielt auf das kanonische öffentliche Repository und funktioniert nach der Veröffentlichung. Bis dahin sollen bestehende Maintainer ihren aktuell autorisierten Checkout verwenden.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

Aktuellen Desktop-Entwicklungsablauf starten:

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite wird automatisch unter `SUPER_DOLPHIN_HOME/super-dolphin.db` angelegt. Mit `SUPER_DOLPHIN_SQLITE_PATH` kann eine andere lokale Datei verwendet werden. PostgreSQL-Umgebungsvariablen sind kein Konfigurationsweg für die Produktdatenbank.

Build und Tests:

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Contributors mit verknüpften Git Worktrees müssen vor dem Bearbeiten den Worktree-lokalen LSP-Peer bauen und verifizieren. Die genauen Befehle stehen unter [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness).

<!-- sd:governance-demo -->
## Reproduzierbarer Governance-Nachweis

Ausgewählte Gates für eine explizit geänderte Datei anzeigen, ohne sie auszuführen:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

Zentrale Governance-Prüfungen des Repositorys ausführen:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

Diese Befehle prüfen Architekturregeln, Guard-Verhalten, generierte Navigation, Project-Map-Drift und das Capability Manifest. Sie gelten für dieses Repository und schlagen bei veralteter Wahrheit fehl, statt sie stillschweigend zu aktualisieren. Explizite `*-refresh`-Targets dürfen nur verwendet werden, wenn die besitzende Quelle absichtlich geändert wurde.

## Codequalität

| Metrik | Aktuelle Wahrheitsquelle |
|---|---|
| Architekturtests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architekturregeln | [Generierte Backend-Boundary-Map](docs/doc/codemap/13-archtest-boundaries.md) |
| Testabdeckung | Aus einem aktuellen Testlauf neu berechnen; es wird kein statischer Prozentsatz behauptet |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## Sicherheit

Keine Zugangsdaten, Provider Homes, lokalen Datenbanken, Logs, User Memory oder maschinenspezifische Konfiguration committen. Fehlende Identität, fehlendes Ownership, fehlende Konfiguration oder Abhängigkeiten müssen geschlossen fehlschlagen, statt sich stillschweigend abzuschwächen.

Schwachstellen über das private Verfahren in der [Security Policy](SECURITY.md) melden. Keine Exploit-Details, Secrets, Trace Payloads oder Nutzerdaten in ein öffentliches Issue schreiben.

<!-- sd:community -->
## Community und Beiträge

Fokussierte Issues und Pull Requests sind willkommen. Einstiegspunkte:

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

KI-unterstützte Beiträge sind willkommen, doch Contributors bleiben für den eingereichten Diff, Tests, Sicherheit, Lizenzierung und Nachweise verantwortlich. Eine generierte Antwort oder ein erfolgreicher Agent Run ersetzt keine Repository-Gates.

## Lizenz

Lizenziert unter der [Apache License 2.0](LICENSE). Hinweise zur Namensnennung des Projekts und Dritter stehen in [NOTICE](NOTICE).
