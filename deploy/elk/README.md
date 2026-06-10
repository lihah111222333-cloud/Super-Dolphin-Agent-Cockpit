# Local ELK for Super Dolphin

This directory runs a local-only Elastic Stack for development log inspection.
It does not change the application runtime. Logstash tails existing files under
`.tmp/**/*.log`, including the logs produced by `run-new-ui-desktop.sh`.

Ports are bound to `127.0.0.1` only:

- Elasticsearch: http://127.0.0.1:9200
- Kibana: http://127.0.0.1:5601

## Start

```powershell
.\scripts\elk-local.ps1 start
```

Then start the app normally. For the current React desktop flow:

```bash
./run-new-ui-desktop.sh
```

In Kibana, create a data view for:

```text
super-dolphin-logs-*
```

Use `@timestamp` as the time field.

## Manage

```powershell
.\scripts\elk-local.ps1 status
.\scripts\elk-local.ps1 logs
.\scripts\elk-local.ps1 stop
.\scripts\elk-local.ps1 down
```

`stop` keeps containers and data volumes. `down` removes the containers and
network but keeps the named volumes.

## Notes

- The stack uses `ELASTIC_STACK_VERSION`, defaulting to `9.4.2`.
- Security is disabled because this is a localhost-only development stack.
- Logstash tries to parse each log line as JSON. If parsing fails, the raw line
  remains available as `message`.
- Older Windows launcher logs can contain non-UTF-8 bytes and may produce
  Logstash charset warnings. The current app JSON log lines are still parsed.
