# Support

## Tiers

| Tier | Channel | Response target | Covers |
|---|---|---|---|
| Community | GitHub issues | best effort | bugs, questions, feature requests |
| Standard | support@del.ma | 2 business days | deployment help, upgrade guidance |
| Priority | support@del.ma (contract) | 4 business hours | incidents on a production control plane |

## What to include

`heimdall version`, the deployment's `heimdall doctor` output, the relevant
`HD`-coded error, and — for sync issues — the operation document from
`/api/v1/projects/{p}/apps/{a}/history`. Never include secret values; HD
error text never contains them by design.

## Supported versions

CalVer `YY.WW.PATCH`. The latest release is supported; the previous release
receives security fixes for 26 weeks after its successor ships.
