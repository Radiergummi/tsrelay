import { CSPProvider } from "@base-ui/react/csp-provider";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { Toaster } from "./components/ui/toast";
import "./index.css";

// Base UI would otherwise render <style> elements, which the CSP blocks;
// every style it needs comes from our classes instead.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <CSPProvider disableStyleElements>
      <Toaster>
        <App />
      </Toaster>
    </CSPProvider>
  </StrictMode>,
);
