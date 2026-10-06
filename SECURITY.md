# Security policy

## Supported versions

Security fixes are made to the latest release of Epoch v2. Epoch v1 has been retired and is not maintained.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub's [security advisory form](https://github.com/HarshalPatel1972/epoch/security/advisories/new).

Include what an attacker can do, the affected package and version, and steps to reproduce. You can expect an acknowledgement within a few days and a fix or mitigation plan within two weeks for confirmed issues. Reporters are credited in the advisory unless they prefer otherwise.

## Deployment notes

- `epochhttp` has no authentication. It exposes your application's full history, including command payloads. Mount it behind your own authentication or bind it to an internal address.
- `epochhttp` is read-only unless `Config.AllowWrites` is set. Its write endpoints only accept JSON bodies or a custom header, which browsers cannot send cross-site without a CORS preflight. Do not add permissive CORS headers in front of it.
- Recorded commands and events are stored as given. Do not record secrets such as passwords or full card numbers in them.
