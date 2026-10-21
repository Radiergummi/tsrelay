---
title: Access control
description: Grant who may receive, send and administer callbacks with your tailnet policy, and scope them with channels.
category: guide
tags: [operators, grants, acl, roles, channels]
---

# Access control

The relay is a tailnet node tagged `tag:tsrelay`. Access to it comes from [grants](https://tailscale.com/kb/1324/grants): the `ip` part lets a device reach the relay at all, and the `app` part, the `mazetti.me/cap/relay` [app capability](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities), says what it may do there. A device without a value for the capability has no access; the web UI tells such users exactly that, and shows a grant that would fix it.

## A minimal policy

```jsonc
"tagOwners": { "tag:tsrelay": ["autogroup:admin"] },
"grants": [
  // Who may receive callbacks
  { "src": ["autogroup:member"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["receiver"] }] } },
  // Who may send callbacks
  { "src": ["tag:prod-worker"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["sender"] }] } }
]
```

## Capability values

Each capability value is an object with these fields:

| Field | |
|---|---|
| `roles` | Any of `receiver`, `sender`, `admin` (see below) |
| `channels` | Channel patterns the roles apply to; `*` matches anything, dots included. Default `["*"]` |
| `maxHooks` | Receivers: hooks owned at once (relay default 10) |
| `maxTunnels` | Receivers: tunnels attached at once (relay default 5) |
| `maxHookTTL` | Receivers: the longest a persistent hook may live, as a Go duration like `720h` (relay default: unlimited) |
| `public` | Receivers: `true` lets them make their hooks reachable from the internet; see [Serving public hooks](running-a-relay.md#serving-public-hooks) |

## Roles

| Role | May |
|---|---|
| `receiver` | Create and manage their own hooks in channels their patterns match, attach tunnels, inspect and replay their own calls |
| `sender` | Call any hook whose channel their patterns match. Tagged machines can only ever be senders |
| `admin` | List, view the metadata of, and delete everyone's hooks and tunnels. Admins **cannot** see other users' captured queries, headers or bodies, and cannot replay their calls |

## Channels

Channels scope senders to receivers:

```jsonc
{ "roles": ["receiver"], "channels": ["staging", "prod.*"] }   // may create hooks in these channels
{ "roles": ["sender"],   "channels": ["staging"] }             // may only call staging hooks
```

Scope sender grants first, because they are the broad ones.

## Combining grants

When several grants match a device, the relay combines them: roles and channels are unioned (channels per role, so a sender grant never widens what the same device may do as a receiver), and each limit is the highest any grant sets, falling back to the relay default. A grant can therefore only raise a limit; to restrict most users, lower the relay default and raise it for specific groups. Revoking a grant takes effect within one `--revalidate-interval` (60 s), also for attached tunnels. The details are in [§5 of the design document](https://github.com/Radiergummi/tsrelay/blob/main/docs/DESIGN.md#5-authorization).

## Making someone an admin

Admin is a role like the others: grant it to a group in its own grant, or add it to an existing one. For example, to let `group:platform` receive callbacks and administer the relay:

```jsonc
"groups": { "group:platform": ["alice@example.com", "bob@example.com"] },
"grants": [
  { "src": ["group:platform"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["receiver", "admin"] }] } }
]
```

Admins get an **Admin** page in the web UI listing every hook and tunnel, as metadata only. An admin without the `receiver` role starts on the admin page and can't create hooks. The role takes effect on the admin's next request; no relay restart is needed.
