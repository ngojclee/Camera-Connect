import "./styles.css";
import { execAction, isWailsRuntime, selectDirectory } from "./bridge";
import { WindowIsMaximised } from "../wailsjs/runtime/runtime";

/* ---------- types ---------- */
interface CameraInfo { id: string; model: string; mode: string; drive_letter?: string; status: string; last_sync?: string; progress?: number; progress_max?: number; current_file?: string }
interface ProfileSnap { id: string; name: string; base_path: string; photo_template: string; video_template: string; file_types?: string[]; backup_enabled: boolean; remote_name?: string; remote_path?: string; active: boolean }
interface AppStatus {
  tray_color: string; status_text: string; service_running: boolean;
  sync_in_progress: boolean; sync_paused: boolean; active_profile: string;
  cameras: CameraInfo[]; files_synced_total: number; jobs_pending: number;
  jobs_failed: number; last_backup?: string; progress: number;
  current_job_name?: string; critical_error?: string;
  version: string; machine_name: string; appdb_connected: boolean; update_available?: string;
}
interface ConfigSnap {
  profiles?: ProfileSnap[]; active_profile?: string;
  scan_mode?: string; poll_interval?: number; sync_mode?: string;
  overwrite_existing?: boolean; auto_sync?: boolean;
  start_with_windows?: boolean; start_minimized?: boolean; minimize_to_tray?: boolean;
  notify_on_connect?: boolean; notify_on_complete?: string;
  machine_name?: string; profile_paths?: Record<string, string>;
  sync_enabled?: boolean; sync_logged_in?: boolean;
}
interface JobInfo { id: number; kind: string; state: string; payload: string; attempts: number; last_error?: string }
interface BackupStatus { staging_dir: string; pending_files: number; total_size?: number; jobs: JobInfo[]; last_run?: string; last_error?: string }
interface AppDBStatus { enabled: boolean; logged_in: boolean; email?: string; device_label?: string; last_sync_at?: string }
interface FileRec { camera_path: string; camera: string; profile: string; size: number; dest: string; synced_at: string }
interface UpdateInfo { current_version: string; latest_version: string; has_update: boolean; release_notes?: string; release_url?: string; asset_url?: string; asset_name?: string; download_in_progress: boolean }

/* ---------- state ---------- */
type PanelID = "dashboard" | "import" | "profiles" | "backup" | "sync" | "history" | "logs" | "settings" | "about";
const NAV: { id: PanelID; label: string; icon: string }[] = [
  { id: "dashboard", label: "Dashboard", icon: "dashboard" },
  { id: "import", label: "Import", icon: "download" },
  { id: "profiles", label: "Profiles", icon: "folder_managed" },
  { id: "backup", label: "Backup", icon: "cloud_upload" },
  { id: "sync", label: "Cloud Sync", icon: "sync" },
  { id: "history", label: "History", icon: "history" },
  { id: "logs", label: "Logs", icon: "terminal" },
  { id: "settings", label: "Settings", icon: "settings" },
  { id: "about", label: "About", icon: "info" },
];

const state: {
  panel: PanelID;
  status?: AppStatus; config?: ConfigSnap;
  backup?: BackupStatus; appdb?: AppDBStatus;
  history: FileRec[]; update?: UpdateInfo;
  logs: { id: number; level: string; message: string }[];
  logCursor: number; logLevel: string;
  editingProfile?: string; notice?: string; noticeErr?: boolean;
  importProfile?: string; // manual-import target — independent of active profile
  histCam?: string; histProfile?: string; histQ?: string;
} = { panel: "dashboard", history: [], logs: [], logCursor: 0, logLevel: "ALL" };

const app = document.getElementById("app")!;
const COLOR: Record<string, string> = { green: "#22c55e", blue: "#0ea5e9", orange: "#f59e0b", red: "#ef4444" };

/* Input drafts survive the periodic innerHTML re-render — without this the
   3s refresh wipes whatever the user is typing (and kills IME composition). */
const drafts: Record<string, string> = {};
const isTextField = (el: EventTarget | null): el is HTMLInputElement | HTMLTextAreaElement =>
  el instanceof HTMLInputElement && !["checkbox", "radio", "button", "submit"].includes(el.type)
  || el instanceof HTMLTextAreaElement;
app.addEventListener("input", e => {
  const el = e.target;
  if (!(el instanceof HTMLInputElement || el instanceof HTMLSelectElement || el instanceof HTMLTextAreaElement) || !el.id) return;
  if (el instanceof HTMLInputElement && el.type === "checkbox") drafts[el.id] = el.checked ? "1" : "0";
  else drafts[el.id] = el.value;
});
function clearDrafts(prefix: string): void { for (const k of Object.keys(drafts)) if (k.startsWith(prefix)) delete drafts[k]; }

function esc(s: string): string { const d = document.createElement("div"); d.textContent = s ?? ""; return d.innerHTML; }
function badge(color: string, text: string): string {
  return `<span class="cc-badge"><span class="dot" style="background:${COLOR[color] ?? "#666"}"></span>${esc(text)}</span>`;
}
function fmtSize(b: number): string {
  if (!b || b <= 0) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0; let v = b;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${u[i]}`;
}
function fmtTime(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? ts : d.toLocaleString();
}
let toastSeq = 0;
function toast(msg: string, err = false): void {
  const id = ++toastSeq;
  state.notice = msg;
  state.noticeErr = err;
  setTimeout(() => { if (id === toastSeq) { state.notice = undefined; render(); } }, 3500);
}
function resultToast(r: { ok: boolean; error?: string }, okMsg: string): void {
  toast(r.ok ? okMsg : (r.error ?? "failed"), !r.ok);
}

/* ---------- panels ---------- */
function panelDashboard(): string {
  const s = state.status, cfg = state.config;
  const cams = s?.cameras ?? [];
  const profiles = cfg?.profiles ?? [];
  return `
  <div class="cc-grid">
    <div class="cc-card cc-stat"><div class="label">Files synced</div><div class="value">${s?.files_synced_total ?? 0}</div></div>
    <div class="cc-card cc-stat"><div class="label">Cameras</div><div class="value">${cams.length}</div></div>
    <div class="cc-card cc-stat"><div class="label">Pending uploads</div><div class="value">${s?.jobs_pending ?? 0}</div></div>
    <div class="cc-card cc-stat"><div class="label">Failed jobs</div><div class="value" style="color:${s?.jobs_failed ? "#f87171" : "inherit"}">${s?.jobs_failed ?? 0}</div></div>
  </div>
  ${s?.critical_error ? `<div class="cc-card cc-alert">${badge("red", "Error")} ${esc(s.critical_error)}</div>` : ""}
  ${s?.sync_in_progress ? `<div class="cc-card"><div class="cc-row"><span class="material-symbols-outlined spin">progress_activity</span><strong>${esc(s.current_job_name || "Syncing…")}</strong>${s.progress >= 0 ? `<div class="cc-progress"><div style="width:${s.progress}%"></div></div>` : ""}</div></div>` : ""}
  <div class="cc-card">
    <h3>Active Profile</h3>
    <div class="cc-row">
      <select class="cc-select" id="profile-select">
        ${profiles.length === 0 ? `<option value="">(no profiles yet)</option>` : ""}
        ${profiles.map(p => `<option value="${esc(p.id)}" ${p.id === cfg?.active_profile ? "selected" : ""}>${esc(p.name)}${p.base_path ? " — " + esc(p.base_path) : ""}</option>`).join("")}
      </select>
      <button class="cc-btn primary" id="btn-scan"><span class="material-symbols-outlined">sync</span>Sync Now</button>
      <button class="cc-btn" id="btn-pause"><span class="material-symbols-outlined">${s?.sync_paused ? "play_arrow" : "pause"}</span>${s?.sync_paused ? "Resume" : "Pause"}</button>
    </div>
  </div>
  <div class="cc-card">
    <h3>Cameras</h3>
    ${cams.length === 0 ? `<div class="cc-empty">No camera connected. Plug in a camera or SD card to begin.</div>` : ""}
    ${cams.map(c => `
      <div class="cc-row cc-row-line">
        <span class="material-symbols-outlined">${c.mode === "mass_storage" ? "sd_card" : "usb"}</span>
        <div><strong>${esc(c.model)}</strong> <span class="cc-muted">${esc(c.mode)}${c.drive_letter ? " · " + esc(c.drive_letter) : ""}</span>
          ${c.status === "syncing" && c.progress_max ? `<div class="cc-muted" style="font-size:11px">${c.progress}/${c.progress_max} — ${esc(c.current_file ?? "")}</div><div class="cc-progress" style="margin-top:4px;max-width:320px"><div style="width:${Math.round(100 * (c.progress ?? 0) / c.progress_max)}%"></div></div>` : ""}
        </div>
        <span class="cc-spacer"></span>
        ${badge(c.status === "syncing" ? "blue" : c.status === "error" ? "red" : "green", c.status)}
      </div>`).join("")}
  </div>
  <div class="cc-card">
    <h3>Recent Activity</h3>
    <div class="cc-log" id="log-view" style="height:160px">${state.logs.slice(-40).map(l => `<div class="lvl-${l.level}">${esc(l.message)}</div>`).join("") || "(waiting for agent…)"}</div>
  </div>`;
}

function panelImport(): string {
  const cams = state.status?.cameras ?? [];
  const profiles = state.config?.profiles ?? [];
  const target = state.importProfile ?? state.config?.active_profile ?? "";
  return `
  <div class="cc-card">
    <h3>Manual Import</h3>
    <div class="cc-empty">One-shot scan → copy new media into the profile chosen below. Independent of auto-sync: it queues after any running job and never changes the active profile. Move mode deletes from camera after copy (mass storage only).</div>
    <div class="cc-row" style="margin-top:10px">
      <label class="cc-muted" style="font-size:12px">Import into</label>
      <select class="cc-select" id="import-profile">
        ${profiles.map(p => `<option value="${esc(p.id)}" ${p.id === target ? "selected" : ""}>${esc(p.name)} — ${esc(p.base_path || "")}</option>`).join("")}
      </select>
      <select class="cc-select" id="import-mode">
        <option value="">Default mode</option><option value="copy">Copy</option><option value="move">Move (MS only)</option>
      </select>
      <button class="cc-btn primary" id="btn-import-all"><span class="material-symbols-outlined">download</span>Import All Cameras</button>
    </div>
    ${profiles.length === 0 ? `<div class="cc-empty" style="margin-top:8px">No profiles yet — create one in Profiles first.</div>` : ""}
  </div>
  <div class="cc-card">
    <h3>Connected</h3>
    ${cams.length === 0 ? `<div class="cc-empty">Nothing connected.</div>` : ""}
    ${cams.map(c => `
      <div class="cc-row cc-row-line">
        <span class="material-symbols-outlined">${c.mode === "mass_storage" ? "sd_card" : "usb"}</span>
        <div><strong>${esc(c.model)}</strong><div class="cc-muted" style="font-size:11px">${esc(c.id)}</div>
          ${c.status === "syncing" && c.progress_max ? `<div class="cc-muted" style="font-size:11px">${c.progress}/${c.progress_max} — ${esc(c.current_file ?? "")}</div><div class="cc-progress" style="margin-top:4px"><div style="width:${Math.round(100 * (c.progress ?? 0) / c.progress_max)}%"></div></div>` : ""}
        </div>
        <span class="cc-spacer"></span>
        ${badge(c.status === "syncing" ? "blue" : "green", c.status)}
        <button class="cc-btn" data-scan="${esc(c.id)}"><span class="material-symbols-outlined">sync</span>Sync</button>
      </div>`).join("")}
  </div>`;
}

function panelProfiles(): string {
  const cfg = state.config;
  const profiles = cfg?.profiles ?? [];
  const paths = cfg?.profile_paths ?? {};
  const editing = profiles.find(p => p.id === state.editingProfile);
  return `
  <div class="cc-card">
    <h3>Profiles</h3>
    <div class="cc-empty">Profiles pick where imported files land and whether they back up to cloud. Per-machine paths override shared ones.</div>
    ${profiles.map(p => `
      <div class="cc-row cc-row-line">
        <span class="material-symbols-outlined">${p.active ? "check_circle" : "radio_button_unchecked"}</span>
        <div><strong>${esc(p.name)}</strong> <span class="cc-muted">(${esc(p.id)})</span>
          <div class="cc-muted" style="font-size:11px">${esc(p.base_path || paths[p.id] || "(no path)")}${p.backup_enabled ? " · backup → " + esc(p.remote_name || "") + ":" + esc(p.remote_path || "") : ""}</div>
        </div>
        <span class="cc-spacer"></span>
        ${p.active ? badge("green", "active") : `<button class="cc-btn" data-activate="${esc(p.id)}">Set active</button>`}
        <button class="cc-btn" data-edit="${esc(p.id)}"><span class="material-symbols-outlined">edit</span></button>
        <button class="cc-btn" data-del="${esc(p.id)}"><span class="material-symbols-outlined">delete</span></button>
      </div>`).join("")}
    <div class="cc-row" style="margin-top:10px"><button class="cc-btn primary" id="btn-add-profile"><span class="material-symbols-outlined">add</span>New Profile</button></div>
  </div>
  ${editing ? `
  <div class="cc-card">
    <h3>Edit Profile — ${esc(editing.name)}</h3>
    <div class="cc-form">
      <label>Name</label><input class="cc-input" id="pf-name" value="${esc(editing.name)}"/>
      <label>Destination (this machine)</label>
      <div class="cc-row"><input class="cc-input" id="pf-path" style="flex:1" value="${esc(paths[editing.id] ?? editing.base_path)}"/><button class="cc-btn" id="pf-browse">Browse…</button></div>
      <label>Photo folder template</label><input class="cc-input" id="pf-ptmpl" value="${esc(editing.photo_template)}"/>
      <label>Video folder template</label><input class="cc-input" id="pf-vtmpl" value="${esc(editing.video_template)}"/>
      <label class="cc-row" style="gap:6px"><input type="checkbox" id="pf-backup" ${editing.backup_enabled ? "checked" : ""}/> Enable cloud backup</label>
      <div id="pf-backup-fields" style="display:${editing.backup_enabled ? "grid" : "none"};gap:8px">
        <label>rclone remote</label><input class="cc-input" id="pf-remote" value="${esc(editing.remote_name ?? "")}" placeholder="gdrive"/>
        <label>Remote path</label><input class="cc-input" id="pf-rpath" value="${esc(editing.remote_path ?? "")}" placeholder="Backup/Photos"/>
      </div>
      <div class="cc-row" style="margin-top:8px">
        <button class="cc-btn primary" id="pf-save" data-id="${esc(editing.id)}">Save</button>
        <button class="cc-btn" id="pf-cancel">Cancel</button>
      </div>
    </div>
  </div>` : ""}`;
}

function panelBackup(): string {
  const b = state.backup;
  return `
  <div class="cc-card">
    <h3>Upload Queue</h3>
    <div class="cc-row">
      ${badge(b?.pending_files ? "orange" : "green", `${b?.pending_files ?? 0} staged file(s)`)}
      ${b?.pending_files ? badge("blue", fmtSize(b?.total_size ?? 0)) : ""}
      ${b?.last_error ? badge("red", esc(b.last_error)) : ""}
      <span class="cc-spacer"></span>
      <button class="cc-btn" id="btn-retry"><span class="material-symbols-outlined">replay</span>Retry failed</button>
    </div>
    <div class="cc-muted" style="font-size:11px;margin-top:6px">Staging: ${esc(b?.staging_dir || "—")} · Last run: ${esc(fmtTime(b?.last_run))}</div>
  </div>
  <div class="cc-card">
    <h3>Jobs</h3>
    ${(b?.jobs ?? []).length === 0 ? `<div class="cc-empty">Queue empty — uploads run automatically after each sync when backup is enabled.</div>` : ""}
    <table class="cc-table"><thead><tr><th>#</th><th>State</th><th>Files</th><th>Remote</th><th>Attempts</th><th>Error</th></tr></thead>
    <tbody>${(b?.jobs ?? []).map(j => { let p: any = {}; try { p = JSON.parse(j.payload || "{}"); } catch { /* malformed */ } return `<tr><td>${j.id}</td><td>${badge(j.state === "failed" ? "red" : j.state === "running" ? "blue" : "orange", j.state)}</td><td>${(p.files ?? []).length}</td><td class="cc-muted">${esc((p.remote ?? "") + ":" + (p.remote_path ?? ""))}</td><td>${j.attempts}</td><td class="cc-muted">${esc(j.last_error || "")}</td></tr>`; }).join("")}</tbody></table>
  </div>`;
}

function panelSync(): string {
  const a = state.appdb;
  const loggedIn = a?.logged_in ?? false;
  return `
  <div class="cc-card">
    <h3>AppDB Account</h3>
    ${!loggedIn ? `
      <div class="cc-empty">Sign in to sync profiles across machines and pull your rclone/Drive config from the encrypted vault.</div>
      <div class="cc-form" style="margin-top:10px;max-width:340px">
        <label>Email</label><input class="cc-input" id="db-email" type="email"/>
        <label>Password</label><input class="cc-input" id="db-pass" type="password"/>
        <button class="cc-btn primary" id="btn-login" style="margin-top:8px"><span class="material-symbols-outlined">login</span>Sign in</button>
      </div>` : `
      <div class="cc-row">
        ${badge("green", "Connected")} <strong>${esc(a?.email ?? "")}</strong>
        <span class="cc-muted">device: ${esc(a?.device_label ?? state.status?.machine_name ?? "")}</span>
        <span class="cc-spacer"></span>
        <button class="cc-btn" id="btn-logout">Sign out</button>
      </div>`}
  </div>
  ${loggedIn ? `
  <div class="cc-card">
    <h3>Credential Vault</h3>
    <div class="cc-empty">rclone.conf is sealed (AES-256-GCM) before it leaves this machine — the server only stores ciphertext. Leave the field empty to use a key derived from your account — same on every machine, nothing to type. Set a custom passphrase for stronger protection.</div>
    <div class="cc-form" style="margin-top:10px;max-width:340px">
      <label>Vault passphrase <span class="cc-muted">(optional — account key if empty)</span></label><input class="cc-input" id="vault-pass" type="password"/>
      <div class="cc-row" style="margin-top:8px">
        <button class="cc-btn" id="btn-vpush"><span class="material-symbols-outlined">upload</span>Push rclone.conf</button>
        <button class="cc-btn" id="btn-vpull"><span class="material-symbols-outlined">download</span>Pull rclone.conf</button>
      </div>
    </div>
  </div>` : ""}`;
}

function panelHistory(): string {
  const cams = [...new Set(state.history.map(r => r.camera))].sort();
  const profs = [...new Set(state.history.map(r => r.profile))].sort();
  const q = (state.histQ ?? "").toLowerCase();
  const rows = state.history.filter(r =>
    (!state.histCam || r.camera === state.histCam) &&
    (!state.histProfile || r.profile === state.histProfile) &&
    (!q || r.camera_path.toLowerCase().includes(q) || r.dest.toLowerCase().includes(q)));
  return `
  <div class="cc-card">
    <h3>Synced Files <span class="cc-muted">(${rows.length}/${state.history.length})</span></h3>
    <div class="cc-row" style="margin:8px 0">
      <select class="cc-select" id="hf-cam"><option value="">All cameras</option>${cams.map(c => `<option ${c === state.histCam ? "selected" : ""}>${esc(c)}</option>`).join("")}</select>
      <select class="cc-select" id="hf-prof"><option value="">All profiles</option>${profs.map(p => `<option ${p === state.histProfile ? "selected" : ""}>${esc(p)}</option>`).join("")}</select>
      <input class="cc-input" id="hf-q" placeholder="Search file/path…" value="${esc(state.histQ ?? "")}" style="flex:1;min-width:140px"/>
    </div>
    ${rows.length === 0 ? `<div class="cc-empty">${state.history.length === 0 ? "Nothing synced yet." : "No matches."}</div>` : ""}
    ${rows.length ? `<table class="cc-table"><thead><tr><th>File</th><th>Camera</th><th>Profile</th><th>Size</th><th>Destination</th><th>Synced</th></tr></thead>
    <tbody>${rows.map(r => `<tr><td>${esc(r.camera_path.split("/").pop() ?? r.camera_path)}</td><td>${esc(r.camera)}</td><td class="cc-muted">${esc(r.profile)}</td><td>${fmtSize(r.size)}</td><td class="cc-muted">${esc(r.dest)}</td><td class="cc-muted">${esc(fmtTime(r.synced_at))}</td></tr>`).join("")}</tbody></table>` : ""}
  </div>`;
}

function panelLogs(): string {
  return `
  <div class="cc-card">
    <div class="cc-row" style="margin-bottom:8px">
      <h3 style="margin:0">Logs</h3>
      <select class="cc-select" id="log-level">
        ${["ALL", "INFO", "WARN", "ERROR", "DEBUG"].map(l => `<option ${state.logLevel === l ? "selected" : ""}>${l}</option>`).join("")}
      </select>
      <span class="cc-spacer"></span>
      <button class="cc-btn" id="btn-copy-logs"><span class="material-symbols-outlined">content_copy</span>Copy</button>
    </div>
    <div class="cc-log" id="log-view" style="height:calc(100vh - 260px)">${state.logs.map(l => `<div class="lvl-${l.level}">${esc(l.message)}</div>`).join("")}</div>
  </div>`;
}

function panelSettings(): string {
  const c = state.config;
  const chk = (id: string, label: string, on?: boolean) =>
    `<label class="cc-row" style="gap:8px"><input type="checkbox" id="${id}" ${on ? "checked" : ""}/> ${label}</label>`;
  return `
  <div class="cc-card">
    <h3>Sync Behavior</h3>
    <div class="cc-form">
      <label>Scan mode</label>
      <select class="cc-select" id="st-scanmode">
        <option value="once" ${c?.scan_mode === "once" ? "selected" : ""}>Once per connection</option>
        <option value="continuous" ${c?.scan_mode === "continuous" ? "selected" : ""}>Continuous (tethered)</option>
      </select>
      <label>Poll interval (seconds)</label><input class="cc-input" id="st-poll" type="number" value="${c?.poll_interval ?? 3}"/>
      <label>Sync mode</label>
      <select class="cc-select" id="st-syncmode">
        <option value="copy" ${c?.sync_mode !== "move" ? "selected" : ""}>Copy (keep on camera)</option>
        <option value="move" ${c?.sync_mode === "move" ? "selected" : ""}>Move (free card — mass storage only)</option>
      </select>
      ${chk("st-overwrite", "Re-copy when destination file differs", c?.overwrite_existing)}
      ${chk("st-autosync", "Auto-sync when camera connects", c?.auto_sync)}
    </div>
  </div>
  <div class="cc-card">
    <h3>Startup & Notifications</h3>
    <div class="cc-form">
      ${chk("st-winstart", "Start with Windows", c?.start_with_windows)}
      ${chk("st-minimized", "Start minimized to tray", c?.start_minimized)}
      ${chk("st-mintray", "Close window → keep running in tray", c?.minimize_to_tray)}
      <label>Notify on complete</label>
      <select class="cc-select" id="st-notify">
        ${["each", "batch", "off"].map(v => `<option ${c?.notify_on_complete === v ? "selected" : ""}>${v}</option>`).join("")}
      </select>
    </div>
  </div>
  <div class="cc-row"><button class="cc-btn primary" id="btn-save-settings"><span class="material-symbols-outlined">save</span>Save Settings</button></div>`;
}

function panelAbout(): string {
  const s = state.status, u = state.update;
  return `
  <div class="cc-card">
    <h3>Camera Connect</h3>
    <div class="cc-muted">Version ${esc(s?.version ?? "dev")} · Machine ${esc(s?.machine_name ?? "")}</div>
    <div class="cc-muted" style="font-size:12px;margin-top:4px">PolyForm Noncommercial — free for personal & hobby use.</div>
    <div class="cc-row" style="margin-top:8px">
      <button class="cc-btn" id="btn-github"><span class="material-symbols-outlined">open_in_new</span>GitHub — ngojclee/camera-connect</button>
    </div>
  </div>
  <div class="cc-card">
    <h3>Updates</h3>
    <div class="cc-row">
      ${u ? (u.has_update ? badge("blue", `v${esc(u.latest_version)} available`) : badge("green", "Up to date")) : badge("green", "Not checked")}
      <button class="cc-btn" id="btn-check-update"><span class="material-symbols-outlined">refresh</span>Check for updates</button>
      ${u?.has_update ? `<button class="cc-btn primary" id="btn-dl-update"><span class="material-symbols-outlined">download</span>Download v${esc(u.latest_version)}</button>` : ""}
    </div>
    ${u?.release_notes ? `
    <div class="cc-changelog">
      <div class="cc-changelog-head">
        <span>What's new in v${esc(u.latest_version)}</span>
        ${u.release_url ? `<a class="cc-link" id="lnk-release" href="#">Full notes</a>` : ""}
      </div>
      <div class="cc-changelog-body">${esc(u.release_notes)}</div>
    </div>` : ""}
  </div>`;
}

/* ---------- render ---------- */
function render(): void {
  // Remember what the user was doing before we rewrite the DOM.
  const active = document.activeElement as HTMLInputElement | null;
  const activeId = active?.id ?? "";
  const selStart = active?.selectionStart ?? null;
  const selEnd = active?.selectionEnd ?? null;

  const s = state.status;
  const online = isWailsRuntime() && s !== undefined;
  const body =
    state.panel === "dashboard" ? panelDashboard() :
    state.panel === "import" ? panelImport() :
    state.panel === "profiles" ? panelProfiles() :
    state.panel === "backup" ? panelBackup() :
    state.panel === "sync" ? panelSync() :
    state.panel === "history" ? panelHistory() :
    state.panel === "logs" ? panelLogs() :
    state.panel === "settings" ? panelSettings() : panelAbout();

  app.innerHTML = `
  <div class="cc-shell">
    <div class="cc-titlebar">
      <div class="cc-row" style="gap:8px">
        <img src="/icon.png" alt="" style="width:22px;height:22px;border-radius:5px"/>
        <strong style="font-family:var(--font-display)">Camera Connect</strong>
        ${badge(s?.tray_color ?? (online ? "green" : "red"), s?.status_text ?? (online ? "Ready" : "Agent offline"))}
        ${s?.update_available ? badge("blue", `v${esc(s.update_available)}`) : ""}
      </div>
      <div class="btns">
        <button class="cc-btn" id="btn-min"><span class="material-symbols-outlined">remove</span></button>
        <button class="cc-btn" id="btn-max"><span class="material-symbols-outlined">crop_square</span></button>
        <button class="cc-btn" id="btn-close"><span class="material-symbols-outlined">close</span></button>
      </div>
    </div>
    <div class="cc-main">
      <nav class="cc-nav">
        ${NAV.map(n => `<button class="cc-nav-item ${state.panel === n.id ? "active" : ""}" data-nav="${n.id}"><span class="material-symbols-outlined">${n.icon}</span>${n.label}</button>`).join("")}
      </nav>
      <div class="cc-body">${body}</div>
    </div>
    ${state.notice ? `<div class="cc-toast${state.noticeErr ? " cc-toast-err" : ""}"><span class="material-symbols-outlined">${state.noticeErr ? "error" : "check_circle"}</span>${esc(state.notice)}</div>` : ""}
  </div>`;

  // Restore input drafts, then focus + caret.
  document.querySelectorAll<HTMLInputElement>("input[id],textarea[id],select[id]").forEach(el => {
    const d = drafts[el.id];
    if (d === undefined) return;
    if (el instanceof HTMLInputElement && el.type === "checkbox") el.checked = d === "1";
    else el.value = d;
  });
  if (activeId) {
    const el = document.getElementById(activeId) as HTMLInputElement | null;
    if (el) {
      el.focus();
      try { if (selStart !== null) el.setSelectionRange(selStart, selEnd); } catch { /* non-text input */ }
    }
  }
  wire();
}

function wire(): void {
  const go = window.go?.main?.WailsApp;
  document.getElementById("btn-min")?.addEventListener("click", () => go?.MinimiseWindow());
  document.getElementById("btn-max")?.addEventListener("click", async () => {
    go?.ToggleMaximise();
    // Reflect square corners when maximised (rounded look only floats).
    setTimeout(syncMaxClass, 150);
  });
  document.getElementById("btn-close")?.addEventListener("click", () => go?.HideToTray());
  document.querySelectorAll("[data-nav]").forEach(el =>
    el.addEventListener("click", () => { state.panel = (el as HTMLElement).dataset.nav as PanelID; lazyLoad(); render(); }));

  // shared
  document.getElementById("btn-scan")?.addEventListener("click", async () => { const r = await execAction("scan-now"); resultToast(r, "Sync queued"); refresh(); });
  document.getElementById("btn-pause")?.addEventListener("click", async () => {
    await execAction(state.status?.sync_paused ? "resume-sync" : "pause-sync"); refresh();
  });
  document.getElementById("profile-select")?.addEventListener("change", async e => {
    const id = (e.target as HTMLSelectElement).value;
    if (id) { await execAction("set-profile", JSON.stringify({ profile_id: id })); toast("Profile switched"); refresh(); }
  });
  document.getElementById("import-profile")?.addEventListener("change", e => {
    state.importProfile = (e.target as HTMLSelectElement).value; // manual target only
  });
  document.getElementById("btn-import-all")?.addEventListener("click", async () => {
    const mode = (document.getElementById("import-mode") as HTMLSelectElement)?.value;
    const profile_id = (document.getElementById("import-profile") as HTMLSelectElement)?.value;
    const r = await execAction("scan-now", JSON.stringify({ ...(mode ? { mode } : {}), ...(profile_id ? { profile_id } : {}) }));
    resultToast(r, "Import started"); refresh();
  });
  document.querySelectorAll("[data-scan]").forEach(el =>
    el.addEventListener("click", async () => {
      const pid = (document.getElementById("import-profile") as HTMLSelectElement)?.value;
      const r = await execAction("scan-now", JSON.stringify({ device_id: (el as HTMLElement).dataset.scan, ...(pid ? { profile_id: pid } : {}) }));
      resultToast(r, "Sync queued"); refresh();
    }));

  // profiles
  document.getElementById("btn-add-profile")?.addEventListener("click", () => {
    const id = `profile_${Date.now().toString(36)}`;
    const cfg = state.config ?? {};
    const profiles = (cfg.profiles ?? []).concat([{ id, name: "New Profile", base_path: "", photo_template: "{camera}/{yyyy}/{yyyy}-{mm}-{dd}", video_template: "{camera}/{yyyy}/{yyyy}-{mm}-{dd}", backup_enabled: false, active: false }]);
    void saveProfiles(profiles, cfg, id);
  });
  document.querySelectorAll("[data-activate]").forEach(el =>
    el.addEventListener("click", async () => {
      await execAction("set-profile", JSON.stringify({ profile_id: (el as HTMLElement).dataset.activate }));
      toast("Profile activated"); refresh();
    }));
  document.querySelectorAll("[data-edit]").forEach(el =>
    el.addEventListener("click", () => { clearDrafts("pf-"); state.editingProfile = (el as HTMLElement).dataset.edit; render(); }));
  document.querySelectorAll("[data-del]").forEach(el =>
    el.addEventListener("click", async () => {
      const id = (el as HTMLElement).dataset.del!;
      if (!confirm(`Delete profile "${id}"?`)) return;
      const cfg = state.config ?? {};
      await saveProfiles((cfg.profiles ?? []).filter(p => p.id !== id), cfg);
    }));
  document.getElementById("pf-cancel")?.addEventListener("click", () => { clearDrafts("pf-"); state.editingProfile = undefined; render(); });
  document.getElementById("pf-backup")?.addEventListener("change", e => {
    const f = document.getElementById("pf-backup-fields"); if (f) f.style.display = (e.target as HTMLInputElement).checked ? "grid" : "none";
  });
  document.getElementById("pf-browse")?.addEventListener("click", async () => {
    const dir = await selectDirectory("Choose destination folder");
    if (dir) (document.getElementById("pf-path") as HTMLInputElement).value = dir;
  });
  document.getElementById("pf-save")?.addEventListener("click", async e => {
    const id = (e.target as HTMLElement).dataset.id!;
    const cfg = state.config ?? {};
    const profiles = (cfg.profiles ?? []).map(p => p.id === id ? {
      ...p,
      name: (document.getElementById("pf-name") as HTMLInputElement).value,
      photo_template: (document.getElementById("pf-ptmpl") as HTMLInputElement).value,
      video_template: (document.getElementById("pf-vtmpl") as HTMLInputElement).value,
      backup_enabled: (document.getElementById("pf-backup") as HTMLInputElement).checked,
      remote_name: (document.getElementById("pf-remote") as HTMLInputElement)?.value ?? "",
      remote_path: (document.getElementById("pf-rpath") as HTMLInputElement)?.value ?? "",
    } : p);
    const paths = { ...(cfg.profile_paths ?? {}) };
    paths[id] = (document.getElementById("pf-path") as HTMLInputElement).value;
    state.editingProfile = undefined;
    clearDrafts("pf-");
    await saveProfiles(profiles, cfg, undefined, paths);
  });

  // backup
  document.getElementById("btn-retry")?.addEventListener("click", async () => {
    const r = await execAction("retry-backups"); resultToast(r, "Requeued"); lazyLoad();
  });

  // appdb
  document.getElementById("btn-login")?.addEventListener("click", async () => {
    const email = (document.getElementById("db-email") as HTMLInputElement).value;
    const password = (document.getElementById("db-pass") as HTMLInputElement).value;
    const r = await execAction("appdb-login", JSON.stringify({ email, password }));
    if (r.ok) clearDrafts("db-"); // don't keep the password draft around
    resultToast(r, "Signed in"); lazyLoad(); render();
  });
  document.getElementById("btn-logout")?.addEventListener("click", async () => {
    await execAction("appdb-logout"); toast("Signed out"); lazyLoad(); render();
  });
  document.getElementById("btn-vpush")?.addEventListener("click", async () => {
    const passphrase = (document.getElementById("vault-pass") as HTMLInputElement).value;
    const r = await execAction("vault-push", JSON.stringify({ passphrase }));
    clearDrafts("vault-");
    resultToast(r, "rclone.conf pushed (sealed)");
  });
  document.getElementById("btn-vpull")?.addEventListener("click", async () => {
    const passphrase = (document.getElementById("vault-pass") as HTMLInputElement).value;
    const r = await execAction("vault-pull", JSON.stringify({ passphrase }));
    clearDrafts("vault-");
    resultToast(r, "rclone.conf pulled + written");
  });

  // history filters — no IPC needed, filters are client-side over loaded rows
  document.getElementById("hf-cam")?.addEventListener("change", e => { state.histCam = (e.target as HTMLSelectElement).value || undefined; render(); });
  document.getElementById("hf-prof")?.addEventListener("change", e => { state.histProfile = (e.target as HTMLSelectElement).value || undefined; render(); });
  document.getElementById("hf-q")?.addEventListener("input", e => { state.histQ = (e.target as HTMLInputElement).value; render(); });

  // logs
  document.getElementById("log-level")?.addEventListener("change", e => { state.logLevel = (e.target as HTMLSelectElement).value; });
  document.getElementById("btn-copy-logs")?.addEventListener("click", () => {
    navigator.clipboard.writeText(state.logs.map(l => `[${l.level}] ${l.message}`).join("\n")); toast("Logs copied");
  });

  // settings
  document.getElementById("btn-save-settings")?.addEventListener("click", async () => {
    const v = (id: string) => (document.getElementById(id) as HTMLInputElement)?.value;
    const c = (id: string) => (document.getElementById(id) as HTMLInputElement)?.checked;
    const payload = {
      scan_mode: v("st-scanmode"), poll_interval: parseInt(v("st-poll") || "3", 10),
      sync_mode: v("st-syncmode"), overwrite_existing: c("st-overwrite"), auto_sync: c("st-autosync"),
      start_with_windows: c("st-winstart"), start_minimized: c("st-minimized"), minimize_to_tray: c("st-mintray"),
      notify_on_complete: v("st-notify"),
    };
    const r = await execAction("save-config", JSON.stringify(payload));
    resultToast(r, "Settings saved"); refresh();
  });

  // about
  document.getElementById("btn-check-update")?.addEventListener("click", async () => {
    const r = await execAction("check-update");
    if (r.ok) { state.update = r.data as UpdateInfo; render(); } else toast(r.error ?? "check failed", true);
  });
  document.getElementById("btn-dl-update")?.addEventListener("click", async () => {
    const r = await execAction("download-update", "{}");
    resultToast(r, "Downloading update…");
  });
  document.getElementById("btn-github")?.addEventListener("click", () => {
    const url = "https://github.com/ngojclee/camera-connect";
    if (window.runtime?.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
    else window.open(url, "_blank");
  });
  document.getElementById("lnk-release")?.addEventListener("click", (e) => {
    e.preventDefault();
    const url = state.update?.release_url;
    if (!url) return;
    if (window.runtime?.BrowserOpenURL) window.runtime.BrowserOpenURL(url);
    else window.open(url, "_blank");
  });
}

async function saveProfiles(profiles: ProfileSnap[], cfg: ConfigSnap, openEdit?: string, paths?: Record<string, string>): Promise<void> {
  // Profiles carry resolved fields; strip UI-only keys back to wire shape.
  const wire = profiles.map(p => ({
    id: p.id, name: p.name, photo_template: p.photo_template, video_template: p.video_template,
    backup: { enabled: p.backup_enabled, remote_name: p.remote_name ?? "", remote_path: p.remote_path ?? "", free_space: false },
  }));
  const payload: Record<string, unknown> = { profiles: wire };
  if (paths) payload.profile_paths = paths;
  const r = await execAction("save-config", JSON.stringify(payload));
  if (openEdit) state.editingProfile = openEdit;
  resultToast(r, "Profiles saved");
  refresh();
}

/* ---------- data loop ---------- */
async function lazyLoad(): Promise<void> {
  if (state.panel === "backup") {
    const r = await execAction("backup-status");
    if (r.ok) state.backup = r.data as BackupStatus;
  } else if (state.panel === "sync") {
    const r = await execAction("appdb-status");
    if (r.ok) state.appdb = r.data as AppDBStatus;
  } else if (state.panel === "history") {
    const r = await execAction("list-history", JSON.stringify({ limit: 300 }));
    if (r.ok) state.history = (r.data as FileRec[]) ?? [];
  }
  render();
}

async function refresh(): Promise<void> {
  const [status, config, logs] = await Promise.all([
    execAction("get-status"),
    execAction("get-config"),
    execAction("subscribe-logs", JSON.stringify({ after_id: state.logCursor, limit: 150 })),
  ]);
  if (status.ok && status.data) state.status = status.data as AppStatus;
  if (config.ok && config.data) state.config = config.data as ConfigSnap;
  if (logs.ok && logs.data) {
    for (const e of (logs.data.entries ?? []) as { id: number; level: string; message: string }[]) {
      state.logCursor = Math.max(state.logCursor, e.id);
      state.logs.push(e);
    }
    if (state.logs.length > 2000) state.logs = state.logs.slice(-1000);
  }
  // Don't rewrite the DOM while the user is typing — drafts still capture
  // values, and the next interaction/user render will paint fresh state.
  if (!isTextField(document.activeElement)) {
    render();
    const lv = document.getElementById("log-view");
    if (lv) lv.scrollTop = lv.scrollHeight;
  }
}

render();
refresh();
setInterval(refresh, 3000);

// Keep rounded corners only while the window floats — Windows expects square
// chrome when maximised or snapped.
async function syncMaxClass(): Promise<void> {
  try {
    document.body.classList.toggle("win-max", await WindowIsMaximised());
  } catch { /* browser preview — no wails runtime */ }
}
window.addEventListener("resize", () => { void syncMaxClass(); });
void syncMaxClass();
