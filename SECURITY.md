# Security Policy

Security reports must be handled privately. Do not disclose vulnerability
details, exploit steps, credentials, user data, private traces, or unredacted
logs in a public issue, pull request, discussion, or commit.

## Report a Vulnerability

After the canonical repository is published and GitHub Private Vulnerability
Reporting is enabled, use this private reporting entry point:

[Privately report a vulnerability](https://github.com/lihah111222333-cloud/super-dolphin-agent/security/advisories/new)

This URL is the publication target, not evidence that the repository or private
reporting channel is already available. Until both are live, follow the fallback
below and do not disclose vulnerability details publicly.

Include enough information for maintainers to reproduce and assess the report:

- a concise summary and expected security impact;
- the affected component, commit SHA, and configuration;
- minimal reproduction steps or a proof of concept;
- relevant logs with secrets, tokens, personal data, and local paths removed;
- known mitigations or a suggested fix, when available; and
- any disclosure constraints that maintainers should understand.

Do not include unrelated repository data or a full private environment dump.

## If Private Vulnerability Reporting Is Unavailable

Do not publish technical details. First check whether the
[repository owner's GitHub profile](https://github.com/lihah111222333-cloud)
currently provides a private contact method. If it does, use that method without
posting the report publicly.

After the canonical issue tracker is public, if no private method is available,
open a
[minimal security contact request](https://github.com/lihah111222333-cloud/super-dolphin-agent/issues/new?title=Private%20security%20contact%20request)
that says only that private vulnerability reporting is unavailable and asks for
a private reporting path. Do not include the affected component, impact,
reproduction, evidence, or identity of affected users in that public request.
Wait for a private path before sharing details. Before publication, if the owner
profile has no private contact method, retain the report privately; there is no
public project channel to use yet.

## Scope and Expectations

Reports about Super Dolphin Agent source code, its desktop runtime, MCP/LSP
sidecars, provider bridges, storage, update or packaging paths, and repository
governance tooling are in scope. Problems limited to an upstream model provider,
account, billing system, or third-party service should be reported to that
provider unless Super Dolphin Agent introduces the vulnerability.

The project does not promise a fixed acknowledgement, remediation, disclosure,
release, or CVE-assignment deadline. Handling depends on impact,
reproducibility, available maintainer capacity, and coordination needs. Keep the
report private until maintainers and the reporter agree that disclosure is
appropriate.

For non-security bugs and usage questions, follow [Support](SUPPORT.md). For
community conduct concerns, follow the [Code of Conduct](CODE_OF_CONDUCT.md);
do not submit conduct reports as security vulnerabilities.
