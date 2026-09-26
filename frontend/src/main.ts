import "./styles.css";
import { execAction, isWailsRuntime, selectDirectory } from "./bridge";

interface CameraInfo { id: string; model: string; mode: string; status: string; last_sync?: string }
interface ProfileSnapshot { id: string; name: string; base_path: string; active: boolean; backup_enabled: boolean }
interface AppStatus {
  tray_color: string; status_text: string; service_running: boolean;
  sync_in_progress: boolean; sync_paused: boolean; active_profile: string;
  cameras: CameraInfo[]; files_synced_total: number; jobs_pending: number;
  jobs_failed: number; version: string; machine_name: string;
  appdb_connected: boolean; update_available?: string;
}
interface ConfigSnapshot {
  profiles?: ProfileSnapshot[]; active_profile?: string;
  scan_mode?: string; sync_mode?: string; auto_sync?: boolean;
}

const app = document.getElementById("app")!;

const state: {
  status?: AppStatus; config?: ConfigSnapshot; logs: string[];
} = { logs: [] };

const COLOR: Record<string, string> = {
  green: "#22c55e", blue: "#0ea5e9", orange: "#f59e0b", red: "#ef4444",
};

function badge(color: string, text: string): string {
  return `<span class="cc-badge"><span class="dot" style="background:${COLOR[color] ?? "#666"}"></span>${esc(text)}</span>`;
}
function esc(s: string): string {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

function render(): void {
  const s = state.status;
  const cfg = state.config;
  const cameras = s?.cameras ?? [];
  const profiles = cfg?.profiles ?? [];

  app.innerHTML = `
  <div class="cc-shell">
    <div class="cc-titlebar">
      <div class="cc-row" style="gap:8px">
        <span class="material-symbols-outlined" style="color:var(--accent,#2563eb)">photo_camera</span>
        <strong style="font-family:var(--font-display)">Camera Connect</strong>
        ${badge(s?.tray_color ?? "green", s?.status_text ?? (isWailsRuntime() ? "Connecting…" : "Agent offline"))}
      </div>
      <div class="btns">
        <button class="cc-btn" id="btn-min" title="Minimize"><span class="material-symbols-outlined">remove</span></button>
        <button class="cc-btn" id="btn-close" title="Close"><span class="material-symbols-outlined">close</span></button>
      </div>
    </div>
    <div class="cc-body">
      <div class="cc-grid">
        <div class="cc-card cc-stat"><div class="label">Files synced</div><div class="value">${s?.files_synced_total ?? 0}</div></div>
        <div class="cc-card cc-stat"><div class="label">Cameras</div><div class="value">${cameras.length}</div></div>
        <div class="cc-card cc-stat"><div class="label">Pending uploads</div><div class="value">${s?.jobs_pending ?? 0}</div></div>
        <div class="cc-card cc-stat"><div class="label">Version</div><div class="value" style="font-size:15px">${esc(s?.version ?? "—")}</div></div>
      </div>

      <div class="cc-card">
        <h3>Profile</h3>
        <div class="cc-row">
          <select class="cc-select" id="profile-select">
            ${profiles.length === 0 ? `<option value="">(no profiles — add one in config.yaml)</option>` : ""}
            ${profiles.map(p => `<option value="${esc(p.id)}" ${p.active ? "selected" : ""}>${esc(p.name)} — ${esc(p.base_path)}</option>`).join("")}
          </select>
          <button class="cc-btn primary" id="btn-scan"><span class="material-symbols-outlined">sync</span>Scan Now</button>
          <button class="cc-btn" id="btn-pause"><span class="material-symbols-outlined">${s?.sync_paused ? "play_arrow" : "pause"}</span>${s?.sync_paused ? "Resume" : "Pause"}</button>
          ${s?.update_available ? badge("blue", `Update v${esc(s.update_available)}`) : ""}
        </div>
      </div>

      <div class="cc-card">
        <h3>Cameras</h3>
        ${cameras.length === 0 ? `<div class="cc-empty">No camera connected. Plug in a camera or SD card to begin.</div>` : ""}
        ${cameras.map(c => `
          <div class="cc-row" style="padding:6px 0;border-top:1px solid var(--border,#333)">
            <span class="material-symbols-outlined">${c.mode === "mass_storage" ? "sd_card" : "usb"}</span>
            <strong>${esc(c.model)}</strong>
            <span style="color:var(--text-secondary,#a0a0a0);font-size:12px">${esc(c.mode)}${c.drive_letter ? " · " + esc(c.drive_letter) : ""}</span>
            <span class="cc-spacer"></span>
            ${badge(c.status === "syncing" ? "blue" : c.status === "error" ? "red" : "green", c.status)}
          </div>`).join("")}
      </div>

      <div class="cc-card">
        <h3>Activity Log</h3>
        <div class="cc-log" id="log-view">${state.logs.slice(-200).join("\n") || "(waiting for agent…)"}</div>
      </div>
    </div>
  </div>`;

  document.getElementById("btn-min")?.addEventListener("click", () => window.go?.main?.WailsApp?.MinimiseWindow());
  document.getElementById("btn-close")?.addEventListener("click", () => window.go?.main?.WailsApp?.HideToTray());
  document.getElementById("btn-scan")?.addEventListener("click", async () => { await execAction("scan-now"); refresh(); });
  document.getElementById("btn-pause")?.addEventListener("click", async () => {
    await execAction(s?.sync_paused ? "resume-sync" : "pause-sync"); refresh();
  });
  document.getElementById("profile-select")?.addEventListener("change", async (e) => {
    const id = (e.target as HTMLSelectElement).value;
    if (id) { await execAction("set-profile", id); refresh(); }
  });
}

let logCursor = 0;
async function refresh(): Promise<void> {
  const [status, config, logs] = await Promise.all([
    execAction("get-status"),
    execAction("get-config"),
    execAction("subscribe-logs", JSON.stringify({ after_id: logCursor, limit: 100 })),
  ]);
  if (status.ok && status.data) state.status = status.data as AppStatus;
  if (config.ok && config.data) state.config = config.data as ConfigSnapshot;
  if (logs.ok && logs.data) {
    const entries = (logs.data.entries ?? []) as { id: number; level: string; message: string }[];
    for (const e of entries) {
      logCursor = Math.max(logCursor, e.id);
      state.logs.push(`[${e.level}] ${e.message}`);
    }
  }
  render();
  const lv = document.getElementById("log-view");
  if (lv) lv.scrollTop = lv.scrollHeight;
}

void selectDirectory; // used by settings page in Phase 2+
render();
refresh();
setInterval(refresh, 3000);
