---
title: Access control
description: Grant who may receive, send and administer callbacks with your tailnet policy, and scope them with channels.
category: guide
tags: [operators, grants, acl, roles, channels]
---

# Access control

The relay is a tailnet node tagged `tag:tsrelay`. Access to it comes from [grants](https://tailscale.com/kb/1324/grants): the `ip` part lets a device reach the relay at all, and the `app` part, the `tsrelay.mazetti.me/cap/relay` [app capability](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities), says what it may do there. A device without a value for the capability has no access; the web UI tells such users exactly that, and shows a grant that would fix it.

## A minimal policy

```jsonc
"tagOwners": { "tag:tsrelay": ["autogroup:admin"] },
"grants": [
  // Who may receive callbacks
  { "src": ["autogroup:member"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "tsrelay.mazetti.me/cap/relay": [{ "roles": ["receiver"] }] } },
  // Who may send callbacks
  { "src": ["tag:prod-worker"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "tsrelay.mazetti.me/cap/relay": [{ "roles": ["sender"] }] } }
]
```

## Capability values

Each capability value is an object with these fields:

| Field | |
|---|---|
| `roles` | Any of `receiver`, `sender`, `admin` (see [Roles](#roles)) |
| `channels` | Channel patterns the roles apply to; `*` matches anything, dots included. Absent means `["*"]`; an empty list `[]` matches nothing (see [Channels](#channels)) |
| `maxHooks` | Receivers: hooks owned at once (relay default 10) |
| `maxTunnels` | Receivers: tunnels attached at once (relay default 5) |
| `maxHookTtl` | Receivers: the longest a persistent hook may live, as a Go duration like `720h`. Takes effect only when the relay sets a limit with `--user-max-hook-ttl` (default: unlimited, and then this field does nothing); the grant then raises that limit |
| `public` | Receivers: `true` lets them make their hooks reachable from the internet; see [Serving public hooks](running-a-relay.md#serving-public-hooks) |
| `requires` | Names of grant features a relay must know to apply this value (see [Combining grants](#combining-grants)). No features exist at 1.0 |

## Roles

| Role | May |
|---|---|
| `receiver` | Create and manage their own hooks in channels their patterns match, attach tunnels, inspect and replay their own calls |
| `sender` | Call any hook whose channel their patterns match. Tagged machines can only ever be senders |
| `admin` | List, view the metadata of, and delete everyone's hooks and tunnels. Admins **can't** see other users' captured queries, headers or bodies, and can't replay their calls |

## Channels

Channels scope senders to receivers:

```jsonc
{ "roles": ["receiver"], "channels": ["staging", "prod.*"] }   // may create hooks in these channels
{ "roles": ["sender"],   "channels": ["staging"] }             // may only call staging hooks
```

Scope sender grants first, because they are the broad ones.

Leaving `channels` out grants every channel, the same as `["*"]`. An explicit empty list, `"channels": []`, matches no channel at all: the value grants nothing for its roles.

## Combining grants

When several grants match a device, the relay combines them: roles and channels are unioned (channels per role, so a sender grant never widens what the same device may do as a receiver), and each limit is the highest any grant sets, falling back to the relay default. Limits count per user, and only from values that grant the `receiver` role: a limit on a sender-only or admin-only value is ignored. A grant can therefore only raise a limit, and `maxHookTtl` can't even do that while the relay has no default lifetime; to restrict most users, lower the relay default and raise it for specific groups.

Each value is checked on its own, and the relay logs why it skips or trims one:

- A value that is invalid, such as one with a bad channel pattern or a negative limit, is skipped; the other values still apply.
- A value that names a feature in `requires` that the relay doesn't know is skipped. This lets a newer policy use features an older relay lacks without that relay applying the value with a narrower meaning than you intended. Grant features are added only in new relay versions.
- An unknown role is logged and dropped; the rest of the value applies.

Revoking a grant takes effect within one `--revalidate-interval` (60 s), also for attached tunnels. The details are in [§5 of the design document](https://github.com/Radiergummi/tsrelay/blob/main/docs/DESIGN.md#5-authorization).

## Making someone an admin

Admin is a role like the others: grant it to a group in its own grant, or add it to an existing one. For example, to let `group:platform` receive callbacks and administer the relay:

```jsonc
"groups": { "group:platform": ["alice@example.com", "bob@example.com"] },
"grants": [
  { "src": ["group:platform"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "tsrelay.mazetti.me/cap/relay": [{ "roles": ["receiver", "admin"] }] } }
]
```

Admins get an **Admin** page in the web UI listing every hook and tunnel, as metadata only. An admin without the `receiver` role starts on the admin page and can't create hooks. The role takes effect on the admin's next request; no relay restart is needed.
