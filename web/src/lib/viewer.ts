import type { Hook, Me } from "@/api/schemas";

/** isUser: a person, not a tagged node. Only users own hooks. */
export const isUser = (me: Me) => Boolean(me.login);
export const isReceiver = (me: Me) => isUser(me) && "receiver" in me.roles;
export const isAdmin = (me: Me) => isUser(me) && "admin" in me.roles;

/**
 * owns decides whether owner-only controls appear. The server enforces
 * ownership on every request; this only keeps the UI honest about it.
 */
export const owns = (me: Me, h: Hook) => isUser(me) && h.owner === me.login;
