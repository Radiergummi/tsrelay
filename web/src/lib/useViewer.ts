import { useOutletContext } from "react-router";
import type { Me } from "@/api/schemas";

/** useViewer is the entry document (GET /), which the layout loads before any page renders. */
export function useViewer(): Me {
  return useOutletContext<Me>();
}
