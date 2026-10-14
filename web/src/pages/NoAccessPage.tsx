import type { Me } from "@/api/schemas";

/** NoAccessPage tells a viewer who can't receive callbacks what is missing, with a grant that fixes it (DESIGN.md §5.2). */
export function NoAccessPage({ me }: { me: Me }) {
  const roles = Object.keys(me.roles).sort();
  if (!me.login) {
    return (
      <section className="max-w-2xl">
        <h1 className="mb-3 text-lg font-medium">You can't receive callbacks here yet</h1>
        <p>
          This is a tagged node ({(me.tags ?? []).join(", ")}). Tagged nodes cannot own hooks
          {roles.length > 0 && `; its grants give it the ${roles.join(", ")} role`}. Open the relay
          from your own device to receive callbacks.
        </p>
      </section>
    );
  }
  const grant = `"grants": [
  {
    "src": [${JSON.stringify(me.login)}],
    "dst": ["tag:tsrelay"],
    "ip":  ["tcp:443"],
    "app": { ${JSON.stringify(me.capName)}: [{ "roles": ["receiver"] }] }
  }
]`;
  return (
    <section className="max-w-2xl space-y-3">
      <h1 className="text-lg font-medium">You can't receive callbacks here yet</h1>
      <p>
        {roles.length > 0 ? (
          <>
            Your grants give you the {roles.join(", ")} role, but not <code>receiver</code>, which
            lets you create hooks and receive callbacks.
          </>
        ) : (
          <>
            Your tailnet policy grants you no access to this relay. The <code>receiver</code> role
            lets you create hooks and receive callbacks.
          </>
        )}
      </p>
      <p>A tailnet admin can add a grant like this to the policy file:</p>
      <pre className="overflow-x-auto rounded-md border bg-muted p-3 text-xs">{grant}</pre>
      <p className="text-muted-foreground">
        Use <code>autogroup:member</code> as the source to let every member receive callbacks. The
        relay reads your grants again on every request.
      </p>
    </section>
  );
}
