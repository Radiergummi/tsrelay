import type { Hook, Me } from "@/api/schemas";

/** isUser: a person, not a tagged node. Only users own hooks. */
export const isUser = (me: Me) => Boolean(me.login);
export const isReceiver = (me: Me) => isUser(me) && "receiver" in me.roles;
export const isAdmin = (me: Me) => isUser(me) && "admin" in me.roles;
/**
 * mayStream: the relay opens event streams for a viewer with any role
 * (access.MayStream). Tagged nodes get none from the UI: they own no hooks.
 */
export const mayStream = (me: Me) =>
  isUser(me) && ["receiver", "sender", "admin"].some((role) => role in me.roles);

/**
 * owns decides whether owner-only controls appear. The server enforces
 * ownership on every request; this only keeps the UI honest about it.
 */
export const owns = (me: Me, hook: Hook) => isUser(me) && hook.owner === me.login;
