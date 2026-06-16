# Legacy Web Harness

`frontend/` is a legacy web-only development harness kept because
`run-new-ui-web.sh` still starts this package.

It is not the current desktop UI source. Use `frontend-app/` for the active
React/Vite UI used by `run-new-ui-desktop.sh`.

Do not delete or replace this directory without first retiring or rewriting
`run-new-ui-web.sh` and checking any tests or scripts that still reference it.
