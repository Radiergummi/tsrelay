import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { InstallCommand } from "@/features/cli/InstallCommand";
import { detectPlatform, downloads } from "@/lib/install";
import { useViewer } from "@/lib/useViewer";

export function CliPage() {
  const me = useViewer();
  return (
    <section className="max-w-3xl space-y-6">
      <div>
        <h1 className="mb-1 text-lg font-medium">Get the CLI</h1>
        <p className="text-muted-foreground">
          {me.version === "dev"
            ? "This relay is a development build, so you'll get the latest release; it may not match this relay."
            : `You'll get tsrelay ${me.version}, the version this relay runs.`}
        </p>
      </div>
      <Tabs defaultValue={detectPlatform()}>
        <TabsList>
          <TabsTrigger value="unix">macOS / Linux</TabsTrigger>
          <TabsTrigger value="windows">Windows</TabsTrigger>
        </TabsList>
        <TabsContent value="unix">
          <InstallCommand platform="unix" />
        </TabsContent>
        <TabsContent value="windows">
          <InstallCommand platform="windows" />
        </TabsContent>
      </Tabs>
      <p className="text-muted-foreground">
        Then run <code>tsrelay http 8080</code> to get a hook whose calls reach port 8080.
      </p>
      <div>
        <h2 className="mb-2 font-medium">Direct downloads</h2>
        <ul className="grid gap-1 sm:grid-cols-2">
          {downloads.map(({ os, arch, label }) => (
            <li key={`${os}/${arch}`}>
              <a href={`/download/${os}/${arch}`} className="text-primary hover:underline">
                {label}
              </a>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
