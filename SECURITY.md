# Security policy

tsrelay sits between your tailnet and your developers' machines, and decides who may receive and send callbacks from Tailscale grants. Problems in that boundary matter, and reports are welcome.

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub's private vulnerability reporting](https://github.com/Radiergummi/tsrelay/security/advisories/new). Please don't open a public issue, pull request or discussion for them.

A useful report says:
- what an attacker can do, and from where (any tailnet node, a node with a particular role, the internet)
- the tsrelay version or commit, and how the relay is configured
- the steps or a proof of concept that shows it

You'll get an answer within a week. Once the problem is confirmed, it is fixed in a new release, and the advisory is published with credit to you unless you'd rather not be named.

## Supported versions

Only the latest release gets security fixes.

## In scope

- Anything that lets a caller act beyond its grants: receive or replay another user's callbacks, see their headers or bodies, attach a tunnel to someone else's hook, or reach the relay's API or UI without the right role.
- Ways around the inspector's redaction, or captured data rendered as markup in the web UI.
- Ways to reach a developer's machine other than through a tunnel it dialled.

## Out of scope

- Your tailnet policy. A grant that gives too much access is a configuration problem, not a tsrelay vulnerability.
- Denial of service by a caller who already holds the sender role. Rate limits and hold queues bound it; they don't prevent it.
