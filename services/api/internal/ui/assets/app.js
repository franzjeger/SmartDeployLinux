// deployserver UI — vanilla JS, no framework, no build step.
// Architecture: hash-router, view modules, API client, modal+toast helpers.

// ---------- helpers ----------

const $  = (q, root = document) => root.querySelector(q);
const $$ = (q, root = document) => Array.from(root.querySelectorAll(q));

const escapeHTML = s => {
  if (s === undefined || s === null) return "";
  return String(s).replace(/[&<>"']/g, c => ({
    "&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"
  }[c]));
};
const trunc = (s, n = 8) => {
  if (!s) return "—";
  s = String(s);
  return s.length > n ? s.slice(0, n) + "…" : s;
};
const fmtTime = iso => {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  const now = Date.now();
  const diff = now - d.getTime();
  const sec = Math.round(diff / 1000);
  if (sec < 60) return sec + "s ago";
  if (sec < 3600) return Math.round(sec / 60) + "m ago";
  if (sec < 86400) return Math.round(sec / 3600) + "h ago";
  return d.toLocaleDateString() + " " + d.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"});
};
const fmtAbsolute = iso => {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString();
};

const stateBadge = state => {
  const cfg = {
    "pending":      ["info", "pending"],
    "bootstrapped": ["info", "bootstrapped"],
    "imaging":      ["warn", "imaging"],
    "post_install": ["warn", "post-install"],
    "completed":    ["ok",   "completed"],
    "failed":       ["err",  "failed"],
    "cancelled":    ["",     "cancelled"],
  }[state] || ["", state || "—"];
  const live = ["pending","bootstrapped","imaging","post_install"].includes(state);
  return `<span class="badge ${cfg[0]} ${live ? "pulsing" : ""}"><span class="dot"></span>${cfg[1]}</span>`;
};

// ---------- API client ----------

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(path, opts);
  let data = null;
  if (r.status !== 204) {
    const text = await r.text();
    try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  }
  if (!r.ok) {
    const msg = (data && data.error) || data || `HTTP ${r.status}`;
    throw new Error(typeof msg === "string" ? msg : JSON.stringify(msg));
  }
  return data;
}

// ---------- toast ----------

function toast(message, kind = "") {
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.textContent = message;
  $("#toast-root").appendChild(el);
  setTimeout(() => {
    el.style.opacity = "0";
    el.style.transition = "opacity 0.2s";
    setTimeout(() => el.remove(), 220);
  }, 4500);
}

// ---------- modal ----------

function openModal({ title, body, primary, secondary, onPrimary, onClose }) {
  const root = $("#modal-root");
  const html = `
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-header">
        <h2>${escapeHTML(title)}</h2>
        <button class="modal-close" aria-label="Close">×</button>
      </div>
      <div class="modal-body">${body}</div>
      <div class="modal-footer">
        ${secondary ? `<button class="btn secondary" data-act="secondary">${escapeHTML(secondary)}</button>` : ""}
        ${primary ? `<button class="btn ${primary.danger ? "danger" : ""}" data-act="primary">${escapeHTML(primary.label)}</button>` : ""}
      </div>
    </div>`;
  root.innerHTML = html;
  root.classList.add("active");
  const close = () => {
    root.classList.remove("active");
    root.innerHTML = "";
    onClose && onClose();
  };
  $(".modal-close", root).addEventListener("click", close);
  root.addEventListener("click", e => { if (e.target === root) close(); });
  if (primary) {
    $('[data-act="primary"]', root).addEventListener("click", async () => {
      try {
        const ok = await onPrimary?.($(".modal", root));
        if (ok !== false) close();
      } catch (e) { toast(e.message, "err"); }
    });
  }
  const sec = $('[data-act="secondary"]', root);
  if (sec) sec.addEventListener("click", close);
  return { close };
}

function confirmModal({ title, message, danger, primaryLabel = "Confirm" }) {
  return new Promise(resolve => {
    let result = false;
    openModal({
      title,
      body: `<p>${escapeHTML(message)}</p>`,
      secondary: "Cancel",
      primary: { label: primaryLabel, danger },
      onPrimary: () => { result = true; },
      onClose: () => resolve(result),
    });
  });
}

// ---------- router ----------

const routes = {};
function route(path, handler) { routes[path] = handler; }

function matchRoute(hash) {
  let path = (hash || "#/").slice(1) || "/";
  // strip query string for matching
  const qi = path.indexOf("?");
  if (qi >= 0) path = path.slice(0, qi);
  if (routes[path]) return { handler: routes[path], params: {} };
  for (const [pattern, handler] of Object.entries(routes)) {
    if (!pattern.includes(":")) continue;
    const re = new RegExp("^" + pattern.replace(/:([a-z]+)/g, "(?<$1>[^/]+)") + "$");
    const m = path.match(re);
    if (m) return { handler, params: m.groups || {} };
  }
  return null;
}

let currentCleanup = null;

async function navigate() {
  const hash = location.hash || "#/";
  const m = matchRoute(hash);
  const path = (hash.slice(1).split("?")[0]) || "/";
  $$(".nav-item").forEach(a => a.classList.toggle("active", a.dataset.route === path));
  if (!$$(".nav-item.active").length) {
    const seg = "/" + (path.split("/")[1] || "");
    $$(`.nav-item[data-route="${seg}"]`).forEach(a => a.classList.add("active"));
  }
  if (currentCleanup) { try { currentCleanup(); } catch {} currentCleanup = null; }
  $("#content").innerHTML = `<div class="page"><div class="empty"><div class="spinner"></div></div></div>`;
  if (!m) {
    $("#content").innerHTML = `<div class="page"><h1>404</h1><p class="muted">No route for <code>${escapeHTML(hash)}</code></p></div>`;
    return;
  }
  try {
    const result = await m.handler(m.params);
    if (typeof result === "function") currentCleanup = result;
  } catch (e) {
    $("#content").innerHTML = `<div class="page"><div class="banner err">${escapeHTML(e.message)}</div></div>`;
  }
}

window.addEventListener("hashchange", navigate);
window.addEventListener("DOMContentLoaded", boot);
$("#refresh-btn")?.addEventListener("click", navigate);

function setBreadcrumb(parts) {
  $("#breadcrumb").innerHTML = parts.map((p, i) => {
    const isLast = i === parts.length - 1;
    return p.href && !isLast
      ? `<a href="${escapeHTML(p.href)}">${escapeHTML(p.label)}</a>`
      : `<span class="${isLast ? "crumb-current" : ""}">${escapeHTML(p.label)}</span>`;
  }).join('<span class="sep">›</span>');
}

// ---------- views: Dashboard ----------

route("/", async () => {
  setBreadcrumb([{label: "Dashboard"}]);
  const [machines, jobs, audit, profiles, images] = await Promise.all([
    api("GET", "/api/v1/machines").catch(() => []),
    api("GET", "/api/v1/jobs?limit=10").catch(() => []),
    api("GET", "/api/v1/audit?since=24h&limit=10").catch(() => []),
    api("GET", "/api/v1/profiles").catch(() => []),
    api("GET", "/api/v1/images").catch(() => []),
  ]);

  const liveJobs = jobs.filter(j => ["pending","bootstrapped","imaging","post_install"].includes(j.state));
  const recentDone = jobs.filter(j => ["completed","failed","cancelled"].includes(j.state));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>Dashboard</h1>
          <p class="subtitle">Active deployments and recent activity.</p>
        </div>
        <div class="page-actions"><a class="btn" href="#/deploy">+ New deployment</a></div>
      </div>

      <div class="cards" style="margin-top: 16px;">
        <div class="card compact"><h3>Machines</h3><div class="stat">${machines.length}</div><div class="stat-sub">registered</div></div>
        <div class="card compact"><h3>Live deployments</h3><div class="stat">${liveJobs.length}</div><div class="stat-sub">in progress</div></div>
        <div class="card compact"><h3>Images</h3><div class="stat">${images.length}</div><div class="stat-sub">in library</div></div>
        <div class="card compact"><h3>Profiles</h3><div class="stat">${profiles.length}</div><div class="stat-sub">configured</div></div>
      </div>

      <div class="split" style="margin-top: 24px;">
        <div>
          <div class="section-title">Live deployments</div>
          ${liveJobs.length ? jobsTable(liveJobs) : `
            <div class="card"><div class="empty">
              <div class="empty-icon">▢</div>
              <div>No deployments running.</div>
              <a class="btn" href="#/deploy">Start one</a>
            </div></div>`}
          <div class="section-title">Recent deployments</div>
          ${recentDone.length ? jobsTable(recentDone) : `<div class="card"><div class="empty muted">No recent deployments.</div></div>`}
        </div>
        <div>
          <div class="section-title">Recent activity</div>
          <div class="card compact">
            ${audit.length ? `
              <table>
                <tbody>
                  ${audit.map(e => `
                    <tr class="no-hover">
                      <td>${stateBadgeForAction(e.action)}<span style="margin-left:8px;">${escapeHTML(e.action)}</span></td>
                      <td class="mono muted small" style="text-align:right;">${escapeHTML(fmtTime(e.at))}</td>
                    </tr>`).join("")}
                </tbody>
              </table>` : `<div class="empty muted">No recent activity.</div>`}
          </div>
          <p class="small muted" style="margin-top: 8px;"><a href="#/audit">View full audit log →</a></p>
        </div>
      </div>
    </div>`;

  if (liveJobs.length) {
    const t = setTimeout(navigate, 5000);
    return () => clearTimeout(t);
  }
});

function stateBadgeForAction(action) {
  if (!action) return "";
  if (action.endsWith(".redeem_failed") || action.endsWith(".failed")) return `<span class="badge err"><span class="dot"></span>fail</span>`;
  if (action.endsWith(".completed") || action.endsWith(".redeemed")) return `<span class="badge ok"><span class="dot"></span>ok</span>`;
  if (action.endsWith(".issued") || action.endsWith(".created")) return `<span class="badge info"><span class="dot"></span>+</span>`;
  if (action.endsWith(".deleted")) return `<span class="badge warn"><span class="dot"></span>-</span>`;
  return `<span class="badge"><span class="dot"></span>·</span>`;
}

function jobsTable(jobs) {
  return `<div class="table-wrap"><table>
    <thead><tr><th>Status</th><th>Machine</th><th>Profile</th><th>Started</th><th></th></tr></thead>
    <tbody>
      ${jobs.map(j => `
        <tr onclick="location.hash='#/jobs/${j.id}'">
          <td>${stateBadge(j.state)}</td>
          <td>${escapeHTML(j.machine_asset_tag || trunc(j.machine_id))}</td>
          <td>${escapeHTML(j.profile_name)}</td>
          <td class="mono small muted">${fmtTime(j.started_at || j.created_at)}</td>
          <td class="actions"><a class="btn small secondary" href="#/jobs/${j.id}">Open</a></td>
        </tr>`).join("")}
    </tbody>
  </table></div>`;
}

// ---------- views: Machines list ----------

let machinesState = { search: "" };

route("/machines", async () => {
  setBreadcrumb([{label: "Machines"}]);
  const [machines, profiles] = await Promise.all([
    api("GET", "/api/v1/machines"),
    api("GET", "/api/v1/profiles").catch(() => []),
  ]);
  const profById = Object.fromEntries(profiles.map(p => [p.id, p.name]));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Machines</h1><p class="subtitle">Registered targets. Click a row for detail.</p></div>
        <div class="page-actions"><button class="btn" id="register-btn">+ Register machine</button></div>
      </div>
      <div class="search">
        <input id="machine-search" placeholder="Search asset tag, MAC, vendor…" value="${escapeHTML(machinesState.search)}">
        <span class="muted small" id="machines-count"></span>
      </div>
      <div class="table-wrap" id="machines-table-wrap"></div>
    </div>`;

  const render = () => {
    const q = machinesState.search.toLowerCase();
    const filtered = machines.filter(m => {
      if (!q) return true;
      const hay = [m.AssetTag, m.MACPrimary, m.Vendor, m.Model, m.ID].filter(Boolean).join(" ").toLowerCase();
      return hay.includes(q);
    });
    $("#machines-count").textContent = `${filtered.length} of ${machines.length}`;
    $("#machines-table-wrap").innerHTML = filtered.length ? `
      <table>
        <thead><tr><th>Asset tag</th><th>MAC</th><th>Vendor / Model</th><th>Profile</th><th>Created</th><th></th></tr></thead>
        <tbody>
          ${filtered.map(m => `
            <tr onclick="location.hash='#/machines/${m.ID}'">
              <td><strong>${escapeHTML(m.AssetTag || "—")}</strong></td>
              <td class="mono">${escapeHTML(m.MACPrimary || "—")}</td>
              <td>${escapeHTML([m.Vendor, m.Model].filter(Boolean).join(" / ") || "—")}</td>
              <td>${escapeHTML(profById[m.DefaultProfileID] || "—")}</td>
              <td class="mono small muted">${fmtTime(m.CreatedAt)}</td>
              <td class="actions"><a class="btn small secondary" href="#/deploy?machine=${m.ID}">Deploy</a></td>
            </tr>`).join("")}
        </tbody>
      </table>` : `<div class="empty"><div class="empty-icon">▢</div>No machines match.</div>`;
  };
  render();
  $("#machine-search").addEventListener("input", e => { machinesState.search = e.target.value; render(); });
  $("#register-btn").addEventListener("click", () => openMachineCreateModal(profiles));
});

function openMachineCreateModal(profiles) {
  const profOptions = `<option value="">(none — assign at deploy time)</option>` +
    profiles.map(p => `<option value="${escapeHTML(p.id)}">${escapeHTML(p.name)}</option>`).join("");
  openModal({
    title: "Register machine",
    body: `
      <form id="machine-form">
        <div class="row">
          <label>Asset tag * <input name="asset_tag" required placeholder="lab-01"></label>
          <label>MAC address <input name="mac_primary" placeholder="aa:bb:cc:dd:ee:ff"></label>
        </div>
        <div class="row">
          <label>Vendor <input name="vendor" placeholder="Dell Inc."></label>
          <label>Model <input name="model" placeholder="Latitude 7440"></label>
        </div>
        <div class="row">
          <label class="full">Default profile
            <select name="default_profile_id">${profOptions}</select>
          </label>
        </div>
      </form>`,
    primary: { label: "Register" },
    secondary: "Cancel",
    onPrimary: async modal => {
      const fd = new FormData($("#machine-form", modal));
      const body = {};
      for (const [k, v] of fd) if (v) body[k] = v;
      await api("POST", "/api/v1/machines", body);
      toast(`Registered ${body.asset_tag}`, "ok");
      navigate();
    },
  });
}

// ---------- views: Machine detail ----------

route("/machines/:id", async ({ id }) => {
  setBreadcrumb([{label:"Machines", href:"#/machines"}, {label: id.slice(0,8)+"…"}]);
  const [m, jobs, profiles] = await Promise.all([
    api("GET", `/api/v1/machines/${id}`),
    api("GET", `/api/v1/jobs?machine=${id}`).catch(() => []),
    api("GET", "/api/v1/profiles").catch(() => []),
  ]);
  const profById = Object.fromEntries(profiles.map(p => [p.id, p.name]));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>${escapeHTML(m.AssetTag || trunc(m.ID))}</h1>
          <p class="subtitle mono small">${escapeHTML(m.ID)}</p>
        </div>
        <div class="page-actions">
          <a class="btn" href="#/deploy?machine=${id}">Deploy</a>
          <button class="btn danger" id="delete-machine">Delete</button>
        </div>
      </div>

      <div class="card" style="margin-top:16px;">
        <dl class="detail-grid">
          <dt>Asset tag</dt>      <dd>${escapeHTML(m.AssetTag || "—")}</dd>
          <dt>MAC (primary)</dt>  <dd class="mono">${escapeHTML(m.MACPrimary || "—")}</dd>
          <dt>SMBIOS UUID</dt>    <dd class="mono">${escapeHTML(m.UUIDSMBIOS || "—")}</dd>
          <dt>Vendor</dt>         <dd>${escapeHTML(m.Vendor || "—")}</dd>
          <dt>Model</dt>          <dd>${escapeHTML(m.Model || "—")}</dd>
          <dt>Default profile</dt><dd>${escapeHTML(profById[m.DefaultProfileID] || "—")}</dd>
          <dt>Registered</dt>     <dd class="mono">${fmtAbsolute(m.CreatedAt)}</dd>
        </dl>
      </div>

      <div class="section-title">Deployment history</div>
      ${jobs.length ? jobsTable(jobs) : `<div class="card"><div class="empty muted">No deployments yet for this machine.</div></div>`}
    </div>`;

  $("#delete-machine").addEventListener("click", async () => {
    const ok = await confirmModal({
      title: "Delete machine",
      message: `Delete ${m.AssetTag || trunc(m.ID)}? This is permanent.`,
      danger: true, primaryLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api("DELETE", `/api/v1/machines/${id}`);
      toast("Machine deleted", "ok");
      location.hash = "#/machines";
    } catch (e) { toast(e.message, "err"); }
  });
});

// ---------- views: Deployment wizard ----------

route("/deploy", async () => {
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const preselectMachine = params.get("machine") || "";

  setBreadcrumb([{label:"New deployment"}]);

  const [machines, profiles] = await Promise.all([
    api("GET", "/api/v1/machines"),
    api("GET", "/api/v1/profiles"),
  ]);

  if (!machines.length) {
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>New deployment</h1></div>
        <div class="card"><div class="empty">
          <div class="empty-icon">▢</div>
          <div>Register at least one machine first.</div>
          <a class="btn" href="#/machines">Go to Machines</a>
        </div></div>
      </div>`;
    return;
  }

  const wizardState = {
    step: 1,
    machine_id: preselectMachine || machines[0].ID,
    profile_id: profiles[0]?.id || "",
    ttl_hours: 4,
    label: "",
  };

  const renderWizard = () => {
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>New deployment</h1>
          <p class="subtitle">Issue a single-use code that the onsite operator types into the bootstrap stick.</p></div>

        <div class="wizard-steps">
          <div class="wizard-step ${wizardState.step >= 1 ? "active" : ""} ${wizardState.step > 1 ? "done" : ""}"><span class="step-num">1</span>Target</div>
          <div class="wizard-step ${wizardState.step >= 2 ? "active" : ""} ${wizardState.step > 2 ? "done" : ""}"><span class="step-num">2</span>Profile</div>
          <div class="wizard-step ${wizardState.step >= 3 ? "active" : ""} ${wizardState.step > 3 ? "done" : ""}"><span class="step-num">3</span>Options</div>
          <div class="wizard-step ${wizardState.step >= 4 ? "active" : ""}"><span class="step-num">4</span>Review</div>
        </div>

        <div class="wizard-pane card" id="wizard-pane">
          ${renderWizardStep(wizardState, machines, profiles)}
        </div>

        <div class="wizard-actions">
          <button class="btn secondary" id="wiz-prev" ${wizardState.step === 1 ? "disabled" : ""}>← Back</button>
          ${wizardState.step < 4
            ? `<button class="btn" id="wiz-next">Next →</button>`
            : `<button class="btn" id="wiz-issue">Issue code</button>`}
        </div>
      </div>`;

    $("#wiz-next")?.addEventListener("click", () => { wizardState.step++; renderWizard(); });
    $("#wiz-prev")?.addEventListener("click", () => { wizardState.step--; renderWizard(); });
    $("#wiz-issue")?.addEventListener("click", () => issueDeployment(wizardState));

    if (wizardState.step === 1) {
      $$("#machine-list .machine-row").forEach(row => {
        row.addEventListener("click", () => { wizardState.machine_id = row.dataset.id; renderWizard(); });
      });
    } else if (wizardState.step === 2) {
      $$("#profile-list .profile-row").forEach(row => {
        row.addEventListener("click", () => { wizardState.profile_id = row.dataset.id; renderWizard(); });
      });
    } else if (wizardState.step === 3) {
      $("#wiz-ttl")?.addEventListener("input", e => wizardState.ttl_hours = parseInt(e.target.value)||4);
      $("#wiz-label")?.addEventListener("input", e => wizardState.label = e.target.value);
    }
  };
  renderWizard();
});

function renderWizardStep(s, machines, profiles) {
  if (s.step === 1) {
    return `
      <p class="muted">Select the machine to deploy.</p>
      <div id="machine-list" class="table-wrap"><table>
        <thead><tr><th></th><th>Asset tag</th><th>MAC</th><th>Vendor / Model</th></tr></thead>
        <tbody>
        ${machines.map(m => `
          <tr class="machine-row" data-id="${m.ID}">
            <td><input type="radio" name="m" ${m.ID === s.machine_id ? "checked" : ""}></td>
            <td><strong>${escapeHTML(m.AssetTag || trunc(m.ID))}</strong></td>
            <td class="mono">${escapeHTML(m.MACPrimary || "—")}</td>
            <td>${escapeHTML([m.Vendor, m.Model].filter(Boolean).join(" / ") || "—")}</td>
          </tr>`).join("")}
        </tbody>
      </table></div>`;
  }
  if (s.step === 2) {
    if (!profiles.length) return `<div class="banner err">No deployment profiles configured.</div>`;
    return `
      <p class="muted">Select the deployment profile (image + answer file template).</p>
      <div id="profile-list" class="table-wrap"><table>
        <thead><tr><th></th><th>Profile</th><th>OS</th><th>Image</th></tr></thead>
        <tbody>
        ${profiles.map(p => `
          <tr class="profile-row" data-id="${p.id}">
            <td><input type="radio" name="p" ${p.id === s.profile_id ? "checked" : ""}></td>
            <td><strong>${escapeHTML(p.name)}</strong></td>
            <td>${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)}</td>
            <td>${escapeHTML(p.image_name)}</td>
          </tr>`).join("")}
        </tbody>
      </table></div>`;
  }
  if (s.step === 3) {
    return `
      <p class="muted">Optional code parameters.</p>
      <div class="row">
        <label>TTL (hours) <input type="number" id="wiz-ttl" value="${s.ttl_hours}" min="1" max="168"></label>
        <label class="full">Label (audit trail) <input id="wiz-label" value="${escapeHTML(s.label)}" placeholder="e.g. sent to alice@branch"></label>
      </div>
      <div class="banner info small">Codes are single-use, expire on first redeem, and rate-limited (5 attempts before lock).</div>`;
  }
  if (s.step === 4) {
    const m = machines.find(x => x.ID === s.machine_id);
    const p = profiles.find(x => x.id === s.profile_id);
    return `
      <p class="muted">Review and issue.</p>
      <dl class="detail-grid">
        <dt>Machine</dt>     <dd><strong>${escapeHTML(m.AssetTag || trunc(m.ID))}</strong> <span class="muted small">(${escapeHTML(m.MACPrimary || "no mac")})</span></dd>
        <dt>Profile</dt>     <dd><strong>${escapeHTML(p.name)}</strong></dd>
        <dt>OS</dt>          <dd>${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)}</dd>
        <dt>TTL</dt>         <dd>${s.ttl_hours} hours</dd>
        <dt>Label</dt>       <dd>${escapeHTML(s.label || "(none)")}</dd>
      </dl>`;
  }
  return "";
}

async function issueDeployment(s) {
  try {
    const r = await api("POST", "/api/v1/deployments/issue", {
      machine_id: s.machine_id,
      profile_id: s.profile_id,
      ttl_seconds: s.ttl_hours * 3600,
      issued_for: s.label || undefined,
    });
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>Code issued</h1></div>
        <div class="code-display">
          <div class="label">Hand this to the onsite operator</div>
          <div class="code-big">${escapeHTML(r.code)}</div>
          <div class="meta">Expires ${escapeHTML(fmtAbsolute(r.expires_at))}</div>
        </div>
        <div class="banner info">
          The operator types this code into the USB bootstrap stick's TUI prompt.
          The code redeems for a single-use Tailscale ephemeral auth key (1h TTL)
          plus a token-bound iPXE chainload URL. Single-use; treat as a credential.
        </div>
        <div class="btn-row">
          <a class="btn" href="#/jobs">View jobs</a>
          <a class="btn secondary" href="#/deploy">Issue another</a>
        </div>
      </div>`;
    toast(`Code issued: ${r.code}`, "ok");
  } catch (e) { toast(e.message, "err"); }
}

// ---------- views: Jobs ----------

route("/jobs", async () => {
  setBreadcrumb([{label:"Jobs"}]);
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const stateFilter = params.get("state") || "";

  const jobs = await api("GET", "/api/v1/jobs?limit=200" + (stateFilter ? `&state=${encodeURIComponent(stateFilter)}` : ""));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Deployment jobs</h1><p class="subtitle">All issued + in-flight + completed deployments.</p></div>
        <div class="page-actions"><a class="btn" href="#/deploy">+ New deployment</a></div>
      </div>
      <div class="filter-chips" style="margin: 0 0 12px 0;">
        ${["", "pending","bootstrapped","imaging","post_install","completed","failed","cancelled"].map(s => `
          <a class="chip ${stateFilter === s ? "active" : ""}" href="#/jobs${s ? `?state=${s}` : ""}">${s || "all"}</a>
        `).join("")}
      </div>
      ${jobs.length ? jobsTable(jobs) : `<div class="card"><div class="empty muted">No jobs match.</div></div>`}
    </div>`;

  if (jobs.some(j => ["pending","bootstrapped","imaging","post_install"].includes(j.state))) {
    const t = setTimeout(navigate, 5000);
    return () => clearTimeout(t);
  }
});

route("/jobs/:id", async ({ id }) => {
  setBreadcrumb([{label:"Jobs", href:"#/jobs"}, {label: id.slice(0,8)+"…"}]);
  const data = await api("GET", `/api/v1/jobs/${id}`);
  const j = data.job;
  const events = data.events || [];

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>Deployment ${stateBadge(j.state)}</h1>
          <p class="subtitle mono small">${escapeHTML(j.id)}</p>
        </div>
        <div class="page-actions">
          <a class="btn secondary" href="#/machines/${j.machine_id}">View machine</a>
        </div>
      </div>

      <div class="card" style="margin-top:16px;">
        <dl class="detail-grid">
          <dt>Machine</dt>    <dd><a href="#/machines/${j.machine_id}">${escapeHTML(j.machine_asset_tag || trunc(j.machine_id))}</a></dd>
          <dt>Profile</dt>    <dd>${escapeHTML(j.profile_name)}</dd>
          <dt>State</dt>      <dd>${stateBadge(j.state)}</dd>
          <dt>Created</dt>    <dd class="mono">${fmtAbsolute(j.created_at)}</dd>
          <dt>Started</dt>    <dd class="mono">${fmtAbsolute(j.started_at)}</dd>
          <dt>Finished</dt>   <dd class="mono">${fmtAbsolute(j.finished_at)}</dd>
        </dl>
      </div>

      <div class="section-title">Event timeline</div>
      <div class="card">
        ${events.length ? `
          <div class="timeline">
            ${events.map(e => `
              <div class="timeline-item ${e.phase === "completed" ? "ok" : (e.phase === "failed" ? "err" : "")}">
                <div class="when">${fmtAbsolute(e.at)} · <span class="muted">${escapeHTML(e.phase)}</span></div>
                <div class="what">${escapeHTML(e.message)}</div>
              </div>`).join("")}
          </div>` : `<div class="empty muted">No events yet — installer hasn't phoned home.</div>`}
      </div>
    </div>`;

  if (["pending","bootstrapped","imaging","post_install"].includes(j.state)) {
    const t = setTimeout(navigate, 4000);
    return () => clearTimeout(t);
  }
});

// ---------- views: Images ----------

route("/images", async () => {
  setBreadcrumb([{label:"Images"}]);
  const imgs = await api("GET", "/api/v1/images");
  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title"><h1>Images</h1>
        <p class="subtitle">Hardware-independent install media. Replace placeholders with real ingested images via <code>deployctl images upload</code>.</p></div>
      ${imgs.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th>Name</th><th>OS</th><th>Arch</th><th>Versions</th><th>Description</th><th>Created</th>
          </tr></thead>
          <tbody>
          ${imgs.map(i => `
            <tr class="no-hover">
              <td><strong>${escapeHTML(i.name)}</strong></td>
              <td>${escapeHTML(i.os_family)} ${escapeHTML(i.os_version)}</td>
              <td><span class="badge">${escapeHTML(i.arch)}</span></td>
              <td>${i.versions_count}</td>
              <td class="muted small">${escapeHTML(i.description || "—")}</td>
              <td class="mono small muted">${fmtAbsolute(i.created_at)}</td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `<div class="card"><div class="empty muted">No images.</div></div>`}
    </div>`;
});

// ---------- views: Sticks ----------

route("/sticks", async () => {
  setBreadcrumb([{label:"Bootstrap sticks"}]);
  const ss = await api("GET", "/api/v1/bootstrap-sticks");
  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title"><h1>Bootstrap sticks</h1>
        <p class="subtitle">Inventory of physical USB sticks. Register after building with <code>make-stick.sh</code>.</p></div>
      ${ss.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th>Built</th><th>SHA-256 (img)</th><th>Tailnet</th><th>Deploy URL</th><th>CA fingerprint</th><th>Label</th>
          </tr></thead>
          <tbody>
          ${ss.map(s => `
            <tr class="no-hover">
              <td class="mono small">${fmtAbsolute(s.BuiltAt)}</td>
              <td class="mono small">${escapeHTML(trunc(s.ImageSHA256, 14))}</td>
              <td>${escapeHTML(s.Tailnet)}</td>
              <td><a href="${escapeHTML(s.DeployURL)}" target="_blank">${escapeHTML(s.DeployURL)}</a></td>
              <td class="mono small">${escapeHTML(trunc(s.CAFingerprint, 18))}</td>
              <td>${escapeHTML((s.Label && (s.Label.String || s.Label)) || "—")}</td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `
        <div class="card"><div class="empty">
          <div class="empty-icon">∎</div>
          <div>No sticks registered.</div>
          <p class="small muted">Build with <code>make -C bootstrap</code>, customize with <code>bootstrap/make-stick.sh</code>, then register via the deployctl CLI.</p>
        </div></div>`}
    </div>`;
});

// ---------- views: Audit ----------

let auditState = { since: "24h", action: "" };

route("/audit", async () => {
  setBreadcrumb([{label:"Audit log"}]);
  const params = new URLSearchParams();
  params.set("since", auditState.since);
  if (auditState.action) params.set("action", auditState.action);
  const events = await api("GET", "/api/v1/audit?" + params);

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title"><h1>Audit log</h1>
        <p class="subtitle">Append-only record of every operator and stick action. Mirrored to <code>/var/log/deployserver/audit.log</code>.</p></div>

      <form id="audit-form" style="margin-bottom: 16px;">
        <div class="row">
          <label>Since
            <select name="since">
              ${["1h","6h","24h","168h","720h"].map(v => `<option value="${v}" ${v===auditState.since?"selected":""}>${v}</option>`).join("")}
            </select>
          </label>
          <label>Action prefix <input name="action" value="${escapeHTML(auditState.action)}" placeholder="auth_code"></label>
          <label>&nbsp;<button type="submit">Apply</button></label>
        </div>
      </form>

      ${events.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th></th><th>Action</th><th>Actor</th><th>Subject</th><th>Source IP</th><th>When</th>
          </tr></thead>
          <tbody>
          ${events.map(e => `
            <tr class="no-hover">
              <td>${stateBadgeForAction(e.action)}</td>
              <td><code>${escapeHTML(e.action)}</code></td>
              <td class="muted small">${escapeHTML(e.actor_kind || "")}<br><span class="mono">${escapeHTML(trunc(e.actor_id))}</span></td>
              <td class="muted small">${escapeHTML(e.subject_kind || "")}<br><span class="mono">${escapeHTML(trunc(e.subject_id))}</span></td>
              <td class="mono small">${escapeHTML(e.source_ip || "—")}</td>
              <td class="mono small muted">${fmtAbsolute(e.at)}</td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `<div class="card"><div class="empty muted">No events match.</div></div>`}
    </div>`;

  $("#audit-form").addEventListener("submit", e => {
    e.preventDefault();
    const fd = new FormData(e.target);
    auditState.since = fd.get("since");
    auditState.action = fd.get("action") || "";
    navigate();
  });
});

// ---------- boot ----------

async function boot() {
  try {
    const me = await api("GET", "/api/v1/me");
    if (me.dev_mode) $("#dev-banner").classList.remove("hidden");
    $("#user-info").textContent = me.user_id && me.user_id !== "00000000-0000-0000-0000-000000000000"
      ? `user ${trunc(me.user_id, 8)}`
      : "anonymous (dev mode)";
  } catch {}
  navigate();
}
