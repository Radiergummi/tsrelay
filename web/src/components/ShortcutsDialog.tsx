import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";

const keys: [string, string][] = [
  ["j / k", "next / previous call"],
  ["Enter / Esc", "select the first call / deselect"],
  ["/", "focus the filter"],
  ["r", "replay"],
  ["c", "copy as curl"],
  ["t", "send a test request"],
  ["?", "show these shortcuts"],
];

export function ShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <table>
          <tbody>
            {keys.map(([k, what]) => (
              <tr key={k}>
                <td className="py-1 pr-6">
                  <kbd className="rounded-sm border px-1.5 text-xs">{k}</kbd>
                </td>
                <td className="py-1">{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <p className="text-xs text-muted-foreground">
          Shortcuts don't fire while you type in a field.
        </p>
      </DialogContent>
    </Dialog>
  );
}
