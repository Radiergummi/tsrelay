import { Link } from "react-router";
import { paths } from "@/api/client";

/** ChannelChip is a channel's name, linking to its page (every user's hooks in it, with all). */
export function ChannelChip({ name, all = false }: { name: string; all?: boolean }) {
  return (
    <Link
      to={paths.channel(name, all)}
      className="rounded-sm bg-muted px-1.5 font-mono text-xs whitespace-nowrap text-muted-foreground hover:text-foreground"
    >
      {name}
    </Link>
  );
}
