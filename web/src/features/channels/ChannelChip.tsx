import { Link } from "react-router";

/** ChannelChip is a channel's name, linking to its page. */
export function ChannelChip({ name }: { name: string }) {
  return (
    <Link
      to={`/channels/${encodeURIComponent(name)}`}
      className="rounded-sm bg-muted px-1.5 font-mono text-xs whitespace-nowrap text-muted-foreground hover:text-foreground"
    >
      {name}
    </Link>
  );
}
