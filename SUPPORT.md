# Support

Super Dolphin Agent is community-supported. The project does not promise a
fixed response, diagnosis, or resolution time.

## Start with the Documentation

- [README and Quick Start](README.md#quick-start)
- [Code map](docs/doc/codemap/README.md)
- [Architecture contracts](docs/%E5%A5%91%E7%BA%A6/README.md)
- [Contribution guide](CONTRIBUTING.md)

Search existing GitHub issues before opening a new request. Build, test, and
guard failures often already contain the exact rule, file, or dependency that
needs attention.

## Choose the Right Channel

- Use the bug report form for a reproducible defect.
- Use the feature request form for a proposed capability or behavior change.
- For a general project question, open a blank issue with a title beginning
  `[Question]` and keep it focused on one topic.
- Use [Security Policy](SECURITY.md) for vulnerabilities. Never disclose a
  vulnerability in a public support request.
- Use the [Code of Conduct](CODE_OF_CONDUCT.md) for community conduct concerns.
  Do not publish incident details when requesting a private conduct channel.

After publication, the canonical repository issue tracker is:

[Super Dolphin Agent issues](https://github.com/lihah111222333-cloud/super-dolphin-agent/issues)

The URL above is the publication target and may not exist before the release
checklist is completed. Before then, existing maintainers should use only their
current authorized project channel; no public support channel is claimed.

## Information to Include

Provide the smallest evidence set that makes the request reproducible:

- the affected area and intended task;
- operating system and architecture;
- Super Dolphin Agent commit SHA;
- Go, Node.js, provider CLI, and relevant language-server versions;
- exact reproduction steps;
- expected and actual behavior;
- exact commands run and their exit status; and
- minimal, redacted logs or diagnostics.

Remove access tokens, credentials, user data, local database contents, private
trace payloads, provider homes, and machine-specific paths before posting.
Prefer a minimal reproduction over a full environment archive.

## Support Boundaries

Project maintainers can investigate code and behavior controlled by Super
Dolphin Agent. They cannot resolve upstream model-provider outages, account or
billing problems, operating-system policy, or failures in third-party services
that the project does not control.

An unanswered request is not evidence that a behavior is supported or that a
fix has been scheduled. Release plans, response times, and compatibility claims
are valid only when documented in the repository and backed by current tests or
release artifacts.
