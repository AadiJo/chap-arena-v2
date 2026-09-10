"use strict";

const byId = (id) => document.getElementById(id);
const stationNames = ["Red 1", "Red 2", "Red 3", "Blue 1", "Blue 2", "Blue 3"];
const networkFields = ["networkSecurityEnabled", "apAddress", "apPassword", "apChannel", "switchAddress", "switchPassword"];
const stationInputs = [];
const stationButtons = [];
const stationRows = [];
const radioLabels = [];
let config;
let draft;
let selectedStation = 0;
let dirty = false;
let settingsDirty = false;
let busy = false;
let status = { applying: false, ap: { state: "idle" }, switch: { state: "idle" } };
let pollTimer;
let pollGeneration = 0;
let monitorAvailable = false;

async function request(path, body) {
  const response = await fetch(path, body === undefined ? { cache: "no-store", signal: AbortSignal.timeout(5000) } : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `Request failed (${response.status}).`);
  return response.json();
}

function showMessage(message = "") {
  byId("message").textContent = message;
  byId("message").hidden = !message;
}

function navigate(path, push = true) {
  if (push) history.pushState(null, "", path);
  const settings = path === "/settings";
  byId("settings-page").hidden = !settings;
  byId("stations-page").hidden = settings;
  byId("stations-link").toggleAttribute("aria-current", !settings);
  byId("settings-link").toggleAttribute("aria-current", settings);
  (settings ? byId("settings-link") : byId("stations-link")).setAttribute("aria-current", "page");
  showMessage();
}
for (const [id, path] of [["stations-link", "/"], ["settings-link", "/settings"]]) {
  byId(id).addEventListener("click", (event) => { event.preventDefault(); navigate(path); });
}
window.addEventListener("popstate", () => navigate(location.pathname, false));
window.addEventListener("beforeunload", (event) => {
  if (dirty || settingsDirty) event.preventDefault();
});

function teamAt(index) {
  const raw = stationInputs[index].value;
  return /^[1-9][0-9]{0,4}$/.test(raw) && Number(raw) <= 25599 ? Number(raw) : 0;
}

function markDirty() {
  dirty = true;
  showMessage();
  renderStatus();
}

function renderSources() {
  stationButtons.forEach((button, index) => {
    const team = teamAt(index);
    button.hidden = !team;
    button.textContent = draft.overrides[team] ? "Override" : "Common";
    button.setAttribute("aria-label", `Edit password for team ${team}`);
    button.setAttribute("aria-pressed", String(selectedStation === index && !!team));
    button.nextElementSibling.hidden = !!team;
    stationRows[index].classList.toggle("selected", selectedStation === index && !!team);
  });
}

function renderEditor() {
  const team = teamAt(selectedStation);
  byId("editor-title").textContent = team ? `Team ${team}` : "Team password";
  byId("editor-empty").hidden = !!team;
  byId("override-controls").hidden = !team;
  byId("override-password").value = draft.overrides[team] || "";
  byId("override-help").textContent = `Saved for team ${team} across stations.`;
  renderSources();
}

function renderRadios() {
  if (!draft) return;
  const labels = { linked: "Radio linked", missing: "No radio", mismatch: "Wrong team", unconfigured: "Not configured", checking: "Checking", unknown: "Unknown", empty: "Empty", "not-applied": "Not applied" };
  let assigned = 0;
  let linked = 0;
  stationInputs.forEach((input, index) => {
    const team = teamAt(index);
    const radio = status.radios?.[index];
    const savedPassword = config.overrides?.[team] || config.commonPassword;
    const draftPassword = draft.overrides[team] || draft.commonPassword;
    let state = "unknown";
    if (!input.value) state = "empty";
    else if (!team || team !== config.stations[index] || savedPassword !== draftPassword) state = "not-applied";
    else if (!monitorAvailable || status.revision !== config.revision) state = "unknown";
    else if (status.ap.state === "applying" || status.ap.state === "accepted") state = "checking";
    else if (radio?.team === team) state = radio.state;
    if (team) assigned++;
    if (state === "linked") linked++;
    stationRows[index].dataset.radioState = state;
    radioLabels[index].dataset.state = state;
    radioLabels[index].textContent = labels[state] || "Unknown";
  });
  byId("radio-count").textContent = `Radios ${linked} / ${assigned} linked`;
}

function renderStatus() {
  const locked = busy || status.applying || !config;
  byId("stations-controls").disabled = locked;
  byId("settings-controls").disabled = locked;
  byId("apply-button").textContent = status.applying ? "Applying..." : "Apply";
  const labels = { idle: "Not applied", pending: "Waiting", applying: "Applying", accepted: "Applying", applied: "Applied", active: "Active", failed: "Failed", unavailable: "Unavailable", unknown: "Unknown", disabled: "Disabled" };
  for (const device of ["ap", "switch"]) {
    const result = monitorAvailable ? status[device] : { state: "unknown" };
    byId(`${device}-status`).textContent = labels[result.state] || result.state;
    byId(`${device}-indicator`).dataset.state = result.state;
    byId(`${device}-detail`).textContent = result.detail || "";
    byId(`${device}-detail`).hidden = !result.detail;
  }
  let summary = "Saved configuration. Press Apply to configure hardware.";
  if (!monitorAvailable) summary = "Waiting for server status...";
  else if (status.applying) summary = "Applying configuration...";
  else if (dirty) summary = "Changes not applied";
  else if (status.revision !== config?.revision) summary = "Configuration changed in another tab. Reload before applying.";
  else if (status.ap.state === "failed" || status.switch.state === "failed") summary = "Configuration incomplete. Press Apply to retry.";
  else if (status.ap.state === "unavailable") summary = "AP status unavailable. Radio links are unknown.";
  else if (status.ap.state === "applying") summary = "Waiting for the AP to become active.";
  else if (status.ap.state === "active" && status.switch.state === "applied") summary = "AP active. Switch configuration applied.";
  if (config && !config.network.networkSecurityEnabled) summary = "Enable advanced network security in Settings before applying.";
  byId("summary").textContent = summary;
  renderRadios();
}

function pausePolling() {
  clearTimeout(pollTimer);
  pollGeneration++;
}

async function pollStatus() {
  pausePolling();
  const generation = pollGeneration;
  try {
    const next = await request("/api/status");
    if (generation !== pollGeneration) return;
    if (next.revision < config.revision) {
      monitorAvailable = false;
    } else {
      status = next;
      monitorAvailable = true;
    }
  } catch {
    if (generation !== pollGeneration) return;
    monitorAvailable = false;
  } finally {
    if (generation === pollGeneration) {
      renderStatus();
      pollTimer = setTimeout(pollStatus, 1000);
    }
  }
}

// A suspended tab must refresh before showing its old radio links as current.
document.addEventListener("visibilitychange", () => {
  if (!config || busy) return;
  monitorAvailable = false;
  renderStatus();
  if (document.hidden) pausePolling();
  else pollStatus();
});

stationNames.forEach((name, index) => {
  const row = document.createElement("tr");
  const label = document.createElement("th");
  label.scope = "row";
  const stationName = document.createElement("span");
  stationName.className = `station-name ${index < 3 ? "red" : "blue"}`;
  stationName.textContent = name;
  const radio = document.createElement("span");
  radio.className = "radio-status";
  radio.dataset.state = "empty";
  radio.textContent = "Empty";
  label.append(stationName, radio);
  radioLabels.push(radio);
  const numberCell = document.createElement("td");
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "numeric";
  input.pattern = "[1-9][0-9]{0,4}";
  input.maxLength = 5;
  input.placeholder = "Empty";
  input.className = "team-number";
  input.setAttribute("aria-label", `${name} team number`);
  input.addEventListener("input", () => {
    selectedStation = index;
    input.setCustomValidity(input.value && !teamAt(index) ? "Enter a team number from 1 to 25599, or leave empty." : "");
    renderEditor();
    markDirty();
  });
  numberCell.append(input);
  const passwordCell = document.createElement("td");
  const button = document.createElement("button");
  button.type = "button";
  button.className = "text-button";
  button.hidden = true;
  button.addEventListener("click", () => {
    selectedStation = index;
    renderEditor();
    byId("override-password").focus();
  });
  const empty = document.createElement("span");
  empty.textContent = "Unassigned";
  empty.className = "unassigned";
  passwordCell.append(button, empty);
  row.append(label, numberCell, passwordCell);
  byId("stations").append(row);
  stationInputs.push(input);
  stationButtons.push(button);
  stationRows.push(row);
});
for (let channel = 5; channel <= 229; channel += 8) {
  byId("apChannel").add(new Option(String(channel), String(channel)));
}

byId("common-password").addEventListener("input", (event) => { draft.commonPassword = event.target.value; markDirty(); });
byId("override-password").addEventListener("input", (event) => {
  const team = teamAt(selectedStation);
  if (!team) return;
  if (event.target.value) draft.overrides[team] = event.target.value;
  else delete draft.overrides[team];
  renderSources();
  markDirty();
});
byId("clear-override").addEventListener("click", () => {
  delete draft.overrides[teamAt(selectedStation)];
  renderEditor();
  markDirty();
});
byId("show-passwords").addEventListener("click", (event) => {
  const show = byId("common-password").type === "password";
  for (const id of ["common-password", "override-password"]) byId(id).type = show ? "text" : "password";
  event.target.textContent = show ? "Hide" : "Show";
  event.target.setAttribute("aria-pressed", String(show));
});
byId("settings-form").addEventListener("input", () => {
  settingsDirty = true;
  showMessage();
});

byId("stations-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (busy || status.applying) return;
  if (settingsDirty) {
    showMessage("Save your networking settings before applying.");
    return;
  }
  busy = true;
  pausePolling();
  showMessage();
  renderStatus();
  try {
    const stations = stationInputs.map((input) => input.value ? Number(input.value) : 0);
    status = await request("/api/apply", {
      revision: config.revision,
      stations,
      commonPassword: draft.commonPassword,
      overrides: draft.overrides,
    });
    config.revision = status.revision;
    config.stations = stations;
    config.commonPassword = draft.commonPassword;
    config.overrides = structuredClone(draft.overrides);
    draft.stations = stations;
    dirty = false;
  } catch (error) {
    showMessage(error.message);
  } finally {
    busy = false;
    renderStatus();
    pollStatus();
  }
});

byId("settings-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (busy || status.applying) return;
  const network = Object.fromEntries(networkFields.map((key) => [key,
    key === "networkSecurityEnabled" ? byId(key).checked : key === "apChannel" ? Number(byId(key).value) : byId(key).value,
  ]));
  busy = true;
  pausePolling();
  showMessage();
  renderStatus();
  try {
    config = await request("/api/settings", { revision: config.revision, network });
    settingsDirty = false;
  } catch (error) {
    showMessage(error.message);
  } finally {
    busy = false;
    renderStatus();
    pollStatus();
  }
});

async function load() {
  navigate(location.pathname, false);
  try {
    config = await request("/api/config");
    draft = structuredClone(config);
    draft.overrides ||= {};
    config.stations.forEach((team, index) => { stationInputs[index].value = team || ""; });
    byId("common-password").value = config.commonPassword;
    for (const key of networkFields) {
      if (key === "networkSecurityEnabled") byId(key).checked = config.network[key];
      else byId(key).value = config.network[key];
    }
    selectedStation = Math.max(0, config.stations.findIndex((team) => team !== 0));
    renderEditor();
    pollStatus();
  } catch (error) {
    showMessage(`Could not load configuration: ${error.message}. Reload to retry.`);
  }
}
load();
