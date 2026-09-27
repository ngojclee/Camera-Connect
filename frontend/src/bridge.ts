// Bridge: thin wrapper over the Wails-bound WailsApp Go methods.
// In dev (vite only, no wails runtime) calls resolve to offline stubs so
// the UI renders without an agent.

export interface ActionEnvelope {
  ok: boolean;
  success: boolean;
  code?: string;
  error?: string;
  data?: any;
}

interface WailsBindings {
  ExecuteAction(action: string, payload: string): Promise<ActionEnvelope>;
  Ping(): Promise<ActionEnvelope>;
  ListCameras(): Promise<ActionEnvelope>;
  SelectDirectory(title: string): Promise<string>;
  ExitApplication(): Promise<ActionEnvelope>;
  HideToTray(): void;
  MinimiseWindow(): void;
  ShowWindow(): void;
}

declare global {
  interface Window {
    go?: { main?: { WailsApp?: WailsBindings } };
    runtime?: { BrowserOpenURL?: (url: string) => void };
  }
}

const bindings = (): WailsBindings | undefined => window.go?.main?.WailsApp;

export function isWailsRuntime(): boolean {
  return bindings() !== undefined;
}

export async function execAction(action: string, payload = ""): Promise<ActionEnvelope> {
  const b = bindings();
  if (!b) {
    return { ok: false, success: false, code: "agent_offline", error: "wails runtime not available" };
  }
  try {
    return await b.ExecuteAction(action, payload);
  } catch (e) {
    return { ok: false, success: false, error: String(e) };
  }
}

export async function selectDirectory(title: string): Promise<string> {
  const b = bindings();
  if (!b) return "";
  try { return await b.SelectDirectory(title); } catch { return ""; }
}
