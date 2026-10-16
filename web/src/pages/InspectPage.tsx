import { Link } from "react-router";
import { EmptyState } from "@/components/PageState";

/** InspectPage is where the server sends an /inspect/ link whose call is gone (or not yours). */
export function InspectPage() {
  return (
    <EmptyState title="This call has expired from the inspector">
      <p className="mb-3">
        The relay keeps only the most recent calls of each hook, in memory, and forgets them when it
        restarts.
      </p>
      <Link to="/hooks" className="text-primary hover:underline">
        Go to your hooks
      </Link>
    </EmptyState>
  );
}
