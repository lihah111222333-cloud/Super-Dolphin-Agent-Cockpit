# Super Dolphin Agent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**KI-native Software-Governance und Kontrollplattform für Multi-Agenten-Entwicklung.**

Super Dolphin Agent ist für Softwareprojekte konzipiert, die überwiegend von KI-Agenten gewartet werden. Die Desktop-Kontrollplattform vereint Multi-Agenten-Sitzungen, Tool-Ausführung, MCP-Orchestrierung, mehrsprachiges LSP, Zeitplanung, Memory, Provider-native Skills, Echtzeit-Ereignisströme und maschinell erzwungene Engineering-Grenzen.

Die englische [README.md](README.md) ist die normative Quelle. Bei Abweichungen in Produktbedeutung, Befehlen, Pfaden, Umgebungsvariablen, Regel-IDs oder Lizenzangaben gilt die englische Fassung.

<!-- sd:why -->
## Für KI-Wartung entworfen

„KI-gewartet“ bedeutet weder ungeprüften Code noch, dass eine KI das gesamte Repository auf einmal verstehen muss. KI ist die primäre Implementierungskraft; das Repository liefert Orientierung, Einschränkungen und Beweise, damit jede Änderung klein und prüfbar bleibt. Menschen behalten die Verantwortung für Produktziele, folgenreiche Entscheidungen, Zugangsdaten und Releases.

Der Wartungszyklus basiert auf begrenztem Kontext:

1. Orientierung durch generierte Code Maps und eine dateibasierte AI project map.
2. Verständnis des Verhaltens durch capability contracts und explizite Architekturkonventionen.
3. Änderung einer kleinen Fläche mit LSP definitions, references, call hierarchies und diagnostics.
4. Begrenzung durch AST-, SSA- und Abhängigkeitsregeln, Komplexitätsbudgets und fail-fast policies.
5. Nachweis durch fokussierte Tests, Prüfungen generierter Artefakte und änderungssensitive Gates vor commit oder push.

Damit entfällt die fragile Annahme, ein Mensch oder eine KI müsse die gesamte Codebasis im Gedächtnis halten.

### Ursprung: Von der V2-Entropie zu Super Dolphin

Super Dolphin Agent begann am 19. März 2026 als saubere Migration von `go-agent-v2`. V2 hatte den Produktwert bereits bewiesen: Agenten-Sitzungen, Tools, Provider, Ereignisse, Recovery und Desktop-Erlebnis funktionierten. Das Problem war nicht fehlende Funktionalität, sondern dass erfolgreiche lokale Erweiterungen die globale Verständlichkeit schrittweise zerstörten.

V2 sammelte mehr als 80 handgeschriebene RPC-Methoden, während binding, validation, capability checks, logging, error mapping und parallele Registrierungswege im System verteilt waren. Die lifecycle-Wahrheit lag in mehreren manager files, einem überlagerten effektiven Zustand, einer impliziten Neben-Zustandsmaschine und asynchronen Recovery-Nebenwirkungen. Ein zentraler event handler wuchs auf 557 Zeilen; der Bus auf Dutzende message/topic-Konstanten; die manuelle Anwendungsverdrahtung auf mehr als 200 Zeilen. Der Code lief weiter, doch die Frage „Wo liegt das maßgebliche Verhalten?“ wurde immer schwerer zu beantworten.

Das Projekt nennt diesen Zustand **Software-Korrosion**. Gemeint sind weder schlechte Entwickler noch zwingend kaputte Ausgaben, sondern ein System, dessen lokale Teile funktionieren, während contracts, ownership und Änderungsgrenzen zu implizitem Wissen werden. Schnelle KI-Iteration verstärkt dies, weil jeder plausible lokale patch einen weiteren versteckten Pfad hinzufügen kann.

Die ursprüngliche V3-Entscheidung lehnte eine Operation am laufenden System mit rund 83.000 Zeilen ab. Das alte System blieb als Verhaltensbeleg bestehen; Fähigkeiten wurden Funktion für Funktion in explizite contracts migriert.

| V2-Entropie | Antwort von Super Dolphin |
|---|---|
| Handgeschriebene RPC-Pfade und verstreute Querschnittslogik | typed requests, ein contract, explizite middleware und error semantics |
| Verteilte lifecycle transitions und Nebenwirkungen | deklarative state transitions, typisierte events und runner mit klarem owner |
| Manueller `New()` / `Close()`-Objektgraph | `fx` composition und explizites Start-/Stop-ownership |
| Kopplung von Fachmodulen an storage/adapters | onion boundaries, Module-owned Ports und anti-corruption adapters |
| Riesenfunktionen mit gemischten Abstraktionsebenen | composed methods und `80 / 4 / 10` für Länge, Verschachtelung und Komplexität |
| Konventionen im Gedächtnis der reviewer | AST/SSA guards, maps, manifests, hooks und reproduzierbare Belege |

V2 ist daher keine zu verbergende Geschichte, sondern das Fehlermodell, gegen das Super Dolphins Governance gebaut ist.

### Technischer Korrosionsschutz gegen AI Code Rot

KI kann Code schnell erzeugen; ohne harte Grenzen verstärkt sie ebenso schnell Architekturdrift. Super Dolphin behandelt diese Drift als **AI Code Rot** und verwandelt sie möglichst nahe am Entstehungsort in maschinenlesbare Fehler.

| Schutzschicht | Was sie verhindert | Nachweis im Repository |
|---|---|---|
| Navigationswahrheit | Bearbeitung des falschen Subsystems oder veraltete mentale Modelle | `docs/doc/codemap`, project map, capability manifest |
| Architekturgrenzen | Direkte Abhängigkeiten von Module zu Store, Provider, UI oder Command | typisiertes Grenzregister und AST-import-Auswertung |
| Semantische Guards | Ignorierte Fehler, silent fallback, unsichere lifecycle patterns, zu breite seams | AST guards und priority SSA analysis |
| Komplexitätsbudget | Riesenfunktionen mit vermischter Fachlogik, Infrastruktur, Protokoll und Persistenz | effektive Funktionslänge `<= 80`, Verschachtelung `<= 4`, zyklomatische Komplexität `<= 10` |
| Schulden-Ratsche | Verschlechterung bekannter Altlasten oder Verschleierung durch neue baseline | production/test freeze weist Regressionen ab und schrumpft bei Verbesserungen |
| Reproduzierbare Gates | Erfolgsaussage ohne Maps, Tests, Artefakte oder exakte Belege | pre-commit, pre-push und änderungssensitive AI maintenance gates |

Die 80-Zeilen-Grenze ist kein Dogma für jedes System. Sie passt zur orchestrierungsintensiven Last dieses Repositorys, bei der composed methods auf einer Abstraktionsebene sicherer sind als monolithische Abläufe. Die tiefere Regel lautet: **Policy sichtbar machen, Details hinter engen interfaces kapseln und Ausnahmen explizit sowie messbar halten.**

### Warum dies kein weiteres Agent Framework ist

| Typisches Agent Framework | Super Dolphin Agent |
|---|---|
| Optimiert Aufgabenausführung | Steuert, wie Aufgaben ein reales Softwaresystem verändern |
| Liefert mehr Tools und Kontext | Liefert begrenzten Kontext, capability contracts und erlaubte Abhängigkeitsrichtungen |
| Wertet ein beendetes run als Erfolg | Verlangt tests, diagnostics, generierten Zustand und Git-Belege |
| Verlässt sich auf Prompt-Disziplin | Erzwingt Invarianten in code, tests, hooks und manifests |
| Verdeckt Fehler mit retries oder defaults | Bricht bei ungültiger Konfiguration, Zustand oder Abhängigkeit fail-fast ab |

```text
intent
  -> code map + capability contract
  -> begrenzte KI-Änderung über LSP/MCP
  -> AST/SSA/architecture guards
  -> focused tests + generated artifact checks
  -> reviewable evidence
  -> accepted commit
```

<!-- sd:architecture -->
## Architekturüberblick

```text
cmd/                 Desktop-Einstiege, MCP-Orchestrierung und mehrsprachiger LSP sidecar
frontend-app/        aktuelle React/Vite Desktop-UI
internal/contract/   modulübergreifende interfaces und DTOs
internal/module/     Fachlogik für Turn, Prompt, Cron, Memory und Skill
internal/platform/   DB, RPC, Konfiguration und Laufzeitsicherheit
internal/provider/   Provider adapter für Codex, Claude CLI und weitere
internal/store/      sqlc-basierte Persistenzadapter und manuelle wrapper
pkg/                 wiederverwendbare öffentliche Bibliotheken
```

Die Fachlogik hängt nur von inneren contracts ab. Store bildet die anti-corruption layer zwischen Domäne und SQL; Provider, MCP und UI liegen außen; `cmd/*` und composition roots verdrahten alles explizit. Siehe [Code Map](docs/doc/codemap/README.md) und [Onion-Architecture-Vertrag](docs/%E5%A5%91%E7%BA%A6/onion-architecture-convention.md).

## Kernfunktionen

- Multi-Agenten-session, resume, fork, scheduling und Echtzeit-Ereignisse.
- MCP-Orchestrierungs-sidecar und generischer mehrsprachiger LSP peer.
- Cron, Memory, Prompt, Thread und Provider-native Skills.
- Adapter für Codex und Claude CLI mit einheitlichem contract.
- SQLite-Persistenz, Wails-Desktop-Host und React/Vite-UI.
- Code map, project map, capability contract, Archtest und AI maintenance gates.

<!-- sd:quick-start -->
## Schnellstart

Voraussetzungen: Go 1.25.7, Node.js 20+, authentifizierte OpenAI Codex CLI, `gopls`, `typescript-language-server` und `typescript@5.9.3`. Claude Code CLI ist nur für den Claude Provider erforderlich.

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

SQLite verwendet standardmäßig `SUPER_DOLPHIN_HOME/super-dolphin.db`. Mit `SUPER_DOLPHIN_SQLITE_PATH` kann eine andere lokale Datei gewählt werden. Kanonische Skills liegen unter `<workspace>/.agents/skills/` und `~/.super-dolphin/skills/personal/{user,agent,imported}/`.

<!-- sd:governance-demo -->
## Reproduzierbarer Governance-Nachweis

Ausgewählte Gates für eine Änderung anzeigen:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

Zentrale Korrosionsschutzprüfungen ausführen:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

Vollständige Prüfung:

```bash
make test
( cd frontend-app && npm run lint && npm test && npm run build )
```

Die Checks sind schreibgeschützt und schlagen fehl, wenn generierte Wahrheit veraltet ist. Verwende die passenden `*-refresh` targets nur bei einer beabsichtigten Aktualisierung.

<!-- sd:security -->
## Sicherheit

- Keine Zugangsdaten, Provider homes, lokalen Datenbanken, logs, user memory oder rechnerbezogene Konfiguration committen.
- Fehlende Konfiguration und defekte Abhängigkeiten folgen dem [fail-fast contract](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md); silent fallback gilt als Fehler.
- Der öffentliche Source exporter liest nur committete Git objects und schließt mit einer default-deny policy interne Pläne, archives, run evidence, lokale workspaces und untracked files aus.
- Sensible Schwachstellen privat an den Repository-Eigentümer melden; keine exploits, secrets oder Nutzerdaten in öffentliche Issues schreiben.

<!-- sd:community -->
## Community und Beiträge

Issues und fokussierte Pull Requests sind willkommen. Änderungen sollen klein und überprüfbar bleiben, Modulgrenzen einhalten, bei fixes regression tests im selben commit enthalten und die passenden Gates ausführen. Architekturentscheidungen gehören in contracts und ausführbare guards, nicht nur in prompts.

- [Code Map](docs/doc/codemap/README.md)
- [Architekturverträge](docs/%E5%A5%91%E7%BA%A6/README.md)
- [Projektanweisungen für Agents](AGENTS.md)
- [Apache License 2.0](LICENSE)

## Lizenz

Lizenziert unter der [Apache License 2.0](LICENSE). Copyright-Hinweise stehen in [NOTICE](NOTICE).
