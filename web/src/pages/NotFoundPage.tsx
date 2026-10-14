import { Link } from "react-router";
import { EmptyState } from "@/components/PageState";

export function NotFoundPage() {
  return (
    <EmptyState title="There is no page here.">
      <Link to="/" className="text-primary hover:underline">
        Go to your hooks
      </Link>
    </EmptyState>
  );
}
