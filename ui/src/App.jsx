import { useEffect, useEffectEvent, useState, lazy, Suspense } from "react";
import { NAV_ITEMS } from "./lib/constants.js";
import {
  apiFetch, normalizeDevice, deviceMapFromList, pushLimited,
  canApprove, parseKeys, emptyCreateForm, emptyDeployForm,
} from "./lib/helpers.js";
import { ToastTray, Modal, Field } from "./components/ui.jsx";
import DashboardPage from "./pages/DashboardPage.jsx";
import DevicesPage from "./pages/DevicesPage.jsx";
import LogsPage from "./pages/LogsPage.jsx";
import DeployPage from "./pages/DeployPage.jsx";

const SSHTerminal = lazy(() => import("./components/SSHTerminal.jsx"));

export default function App() {
  /* ─── navigation ─── */
  const [page, setPage] = useState("dashboard");
  const [pageArg, setPageArg] = useState(null);

  function navigate(target, arg = null) {
    setPage(target);
    setPageArg(arg);
  }

  /* ─── core state ─── */
  const [connected, setConnected] = useState(false);
  const [events, setEvents] = useState([]);
  const [mqttEvents, setMqttEvents] = useState([]);
  const [devices, setDevices] = useState({});
  const [stats, setStats] = useState({
    total_devices: 0, pending: 0, enrolled: 0, operational: 0,
    connected: 0, revoked: 0, total_mqtt: 0, total_events: 0,
  });
  const [pkiInfo, setPkiInfo] = useState({});
  const [selectedDeviceId, setSelectedDeviceId] = useState("");
  const [details, setDetails] = useState({ events: [], vault: null, simulate: null, containers: null, sshProxy: null });
  const [detailLoading, setDetailLoading] = useState(false);
  const [toasts, setToasts] = useState([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [createForm, setCreateForm] = useState(emptyCreateForm);
  const [confirmState, setConfirmState] = useState(null);
  const [working, setWorking] = useState(false);
  const [sshTerminalDevice, setSSHTerminalDevice] = useState(null);

  const pendingDevices = Object.values(devices).filter((d) => canApprove(d));

  /* ─── toasts ─── */
  const addToast = useEffectEvent((message, tone = "info") => {
    const id = crypto.randomUUID();
    setToasts((cur) => [...cur, { id, message, tone }]);
    window.setTimeout(() => setToasts((cur) => cur.filter((t) => t.id !== id)), 3600);
  });

  /* ─── data fetching ─── */
  const refreshSnapshot = useEffectEvent(async () => {
    const [nextStats, nextDevices] = await Promise.all([
      apiFetch("/api/v1/stats"),
      apiFetch("/api/v1/devices"),
    ]);
    setStats((cur) => ({ ...cur, ...nextStats }));
    setDevices(deviceMapFromList(nextDevices));
    setSelectedDeviceId((cur) => {
      if (cur && nextDevices.some((d) => d.device_id === cur)) return cur;
      return nextDevices[0]?.device_id || "";
    });
  });

  const refreshDetails = useEffectEvent(async (deviceId) => {
    if (!deviceId) {
      setDetails({ events: [], vault: null, simulate: null, containers: null, sshProxy: null });
      return;
    }
    setDetailLoading(true);
    try {
      const [eventList, vault, simulate, containers, sshProxy] = await Promise.all([
        apiFetch(`/api/v1/devices/${deviceId}/events?limit=12`).catch(() => []),
        apiFetch(`/api/v1/devices/${deviceId}/vault`).catch(() => null),
        apiFetch(`/api/v1/devices/${deviceId}/simulate`).catch(() => null),
        apiFetch(`/api/v1/devices/${deviceId}/containers`).catch(() => null),
        apiFetch(`/api/v1/devices/${deviceId}/ssh-connect`).catch(() => null),
      ]);
      setDetails({ events: eventList, vault, simulate, containers, sshProxy });
    } finally {
      setDetailLoading(false);
    }
  });

  /* ─── SSE ─── */
  useEffect(() => {
    const source = new EventSource("/events");
    source.onopen = () => setConnected(true);
    source.onerror = () => setConnected(false);

    source.addEventListener("init", (ev) => {
      const p = JSON.parse(ev.data);
      setStats((cur) => ({ ...cur, ...(p.stats || {}) }));
      setDevices(deviceMapFromList(p.devices || []));
      setSelectedDeviceId((cur) => cur || p.devices?.[0]?.device_id || "");
    });

    source.addEventListener("event", (ev) => {
      const p = JSON.parse(ev.data);
      setEvents((cur) => pushLimited(cur, p, 1000));
      if (p.level === "MQTT") setMqttEvents((cur) => pushLimited(cur, p, 300, true));
      setStats((cur) => ({
        ...cur,
        total_events: (cur.total_events || 0) + 1,
        total_mqtt: cur.total_mqtt + (p.level === "MQTT" ? 1 : 0),
      }));
      if (!p.device_id) return;
      setDevices((cur) => {
        const existing = normalizeDevice(cur[p.device_id] || { device_id: p.device_id, status: "active", phase: "pending" });
        return {
          ...cur,
          [p.device_id]: {
            ...existing,
            last_seen: p.timestamp,
            phase: p.phase || existing.phase,
            mqtt_messages: (existing.mqtt_messages || 0) + (p.level === "MQTT" ? 1 : 0),
            cert_serial: p.data?.serial || existing.cert_serial,
            cert_thumbprint: p.data?.thumbprint || existing.cert_thumbprint,
            connection_alive: p.phase === "health" ? true : existing.connection_alive,
          },
        };
      });
    });

    return () => source.close();
  }, []);

  /* ─── polling ─── */
  useEffect(() => {
    refreshSnapshot().catch((e) => addToast(e.message, "danger"));
    apiFetch("/api/v1/pki/info").then((r) => setPkiInfo(r)).catch(() => {});
    const id = window.setInterval(() => refreshSnapshot().catch(() => {}), 5000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    refreshDetails(selectedDeviceId).catch(() => {});
  }, [selectedDeviceId]);

  /* ─── actions ─── */
  async function performAction(path, init, successMsg) {
    setWorking(true);
    try {
      await apiFetch(path, init);
      addToast(successMsg, "good");
      await refreshSnapshot();
      await refreshDetails(selectedDeviceId);
    } catch (e) {
      addToast(e.message, "danger");
    } finally {
      setWorking(false);
    }
  }

  function requestConfirm(cfg) {
    setConfirmState({ ...cfg, inputValue: cfg.inputValue || "" });
  }

  async function submitConfirm() {
    if (!confirmState) return;
    const { onConfirm, inputValue } = confirmState;
    setConfirmState(null);
    await onConfirm(inputValue);
  }

  async function handleControl(deviceId, action) {
    if (action === "revoke") {
      requestConfirm({
        title: "Revoke device",
        body: `Revoke ${deviceId} and invalidate its certificate?`,
        tone: "danger",
        inputLabel: "Reason",
        inputValue: "operator requested revocation",
        onConfirm: async (reason) => {
          await performAction(`/api/v1/devices/${deviceId}/revoke`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ reason }),
          }, `Device ${deviceId} revoked`);
        },
      });
      return;
    }
    if (action === "reboot") {
      requestConfirm({
        title: "Reset device",
        body: `Move ${deviceId} back to pending approval?`,
        tone: "warn",
        onConfirm: async () => {
          await performAction(`/api/v1/devices/${deviceId}/reboot`, { method: "POST" }, `Device ${deviceId} reset to pending`);
        },
      });
      return;
    }
    await performAction(`/api/v1/devices/${deviceId}/${action}`, { method: "POST" }, `Device ${deviceId} approved`);
  }

  async function handleBatchApprove() {
    if (!pendingDevices.length) {
      addToast("No pending devices", "info");
      return;
    }
    requestConfirm({
      title: "Approve pending devices",
      body: `Approve ${pendingDevices.length} pending device${pendingDevices.length === 1 ? "" : "s"}?`,
      tone: "good",
      onConfirm: async () => {
        setWorking(true);
        try {
          await Promise.all(pendingDevices.map((d) => apiFetch(`/api/v1/devices/${d.device_id}/approve`, { method: "POST" })));
          addToast(`Approved ${pendingDevices.length} device${pendingDevices.length === 1 ? "" : "s"}`, "good");
          await refreshSnapshot();
        } catch (e) {
          addToast(e.message, "danger");
        } finally {
          setWorking(false);
        }
      },
    });
  }

  async function handlePurgeLegacy() {
    requestConfirm({
      title: "Purge legacy devices",
      body: "Remove devices without NIC or authorized key metadata?",
      tone: "warn",
      onConfirm: async () => {
        await performAction("/api/v1/devices?mode=legacy", { method: "DELETE" }, "Legacy devices removed");
      },
    });
  }

  async function handleSimulator(deviceId) {
    setWorking(true);
    try {
      await apiFetch(`/api/v1/devices/${deviceId}/simulate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      addToast(`Simulator started for ${deviceId}`, "good");
      await refreshSnapshot();
      await refreshDetails(deviceId);
    } catch (e) {
      if (e.message.includes("local simulator is not enabled")) {
        addToast(`Simulator disabled. Run DEVICE_ID=${deviceId} SERVER_URL=${window.location.origin} go run ./cmd/agent`, "warn");
      } else {
        addToast(e.message, "danger");
      }
    } finally {
      setWorking(false);
    }
  }

  async function handleRemoveSimulator(deviceId) {
    requestConfirm({
      title: "Remove simulator",
      body: `Remove the local simulator container for ${deviceId}?`,
      tone: "danger",
      onConfirm: async () => {
        await performAction(`/api/v1/devices/${deviceId}/simulate`, { method: "DELETE" }, `Simulator removed for ${deviceId}`);
      },
    });
  }

  async function handleRemoveContainer(deviceId, name) {
    requestConfirm({
      title: "Remove container",
      body: `Remove ${name} from ${deviceId}?`,
      tone: "danger",
      onConfirm: async () => {
        await performAction(`/api/v1/devices/${deviceId}/containers/${encodeURIComponent(name)}`, { method: "DELETE" }, `Container ${name} removed`);
      },
    });
  }

  async function handleRequestManifest(kind) {
    if (!selectedDeviceId) return;
    const path = kind === "os"
      ? `/api/v1/deploy/${selectedDeviceId}/os`
      : `/api/v1/config/${selectedDeviceId}/ssh`;
    const label = kind === "os" ? "OS manifest sent" : "SSH config sent";
    await performAction(path, {}, `${label} to ${selectedDeviceId}`);
  }

  async function handleSSHKeys(deviceId) {
    setWorking(true);
    try {
      const result = await apiFetch(`/api/v1/devices/${deviceId}/ssh-keys`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ comment: `operator@${deviceId}` }),
      });
      // Trigger download of private key
      const blob = new Blob([result.private_key_pem], { type: "application/x-pem-file" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `edgeflux_${deviceId}_key`;
      a.click();
      URL.revokeObjectURL(url);
      addToast(`SSH key generated for ${deviceId} (${result.fingerprint}) — private key downloaded`, "good");
      await refreshSnapshot();
      await refreshDetails(deviceId);
    } catch (e) {
      addToast(e.message, "danger");
    } finally {
      setWorking(false);
    }
  }

  async function handleSSHConnect(deviceId) {
    setWorking(true);
    try {
      const result = await apiFetch(`/api/v1/devices/${deviceId}/ssh-connect`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      addToast(`SSH proxy running on port ${result.host_port}`, "good");
      setSSHTerminalDevice(deviceId);
      await refreshSnapshot();
      await refreshDetails(deviceId);
    } catch (e) {
      addToast(e.message, "danger");
    } finally {
      setWorking(false);
    }
  }

  async function handleStopSSH(deviceId) {
    requestConfirm({
      title: "Stop SSH proxy",
      body: `Stop the SSH proxy container for ${deviceId}?`,
      tone: "warn",
      onConfirm: async () => {
        await performAction(`/api/v1/devices/${deviceId}/ssh-connect`, { method: "DELETE" }, `SSH proxy stopped for ${deviceId}`);
      },
    });
  }

  function updateCreateNic(index, field, value) {
    setCreateForm((cur) => ({
      ...cur,
      nics: cur.nics.map((nic, i) => i === index ? { ...nic, [field]: value } : nic),
    }));
  }

  async function handleCreateDevice(event) {
    event.preventDefault();
    setWorking(true);
    try {
      const payload = {
        device_id: createForm.deviceId.trim(),
        profile: createForm.profile.trim() || "alpine-edge-secure",
        simulate: createForm.simulate,
        nics: createForm.nics.map((nic) => ({ ...nic, vlan: Number(nic.vlan || 0) })),
        authorized_keys: parseKeys(createForm.keysText),
      };
      await apiFetch("/api/v1/devices", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      addToast(`Device ${payload.device_id} created`, "good");
      setCreateOpen(false);
      setSelectedDeviceId(payload.device_id);
      navigate("devices", payload.device_id);
      await refreshSnapshot();
      await refreshDetails(payload.device_id);
    } catch (e) {
      addToast(e.message, "danger");
    } finally {
      setWorking(false);
    }
  }

  /* ─── page router ─── */
  function renderPage() {
    switch (page) {
      case "devices":
        return (
          <DevicesPage
            devices={devices}
            selectedDeviceId={pageArg || selectedDeviceId}
            setSelectedDeviceId={(id) => { setSelectedDeviceId(id); setPageArg(id); }}
            details={details}
            detailLoading={detailLoading}
            pkiInfo={pkiInfo}
            working={working}
            onControl={handleControl}
            onSimulator={handleSimulator}
            onRemoveSimulator={handleRemoveSimulator}
            onRemoveContainer={handleRemoveContainer}
            onRequestManifest={handleRequestManifest}
            onOpenDeploy={(id) => navigate("deploy", id)}
            onNavigate={navigate}
            onSSHConnect={handleSSHConnect}
            onSSHKeys={handleSSHKeys}
            onStopSSH={handleStopSSH}
            onOpenTerminal={(id) => setSSHTerminalDevice(id)}
          />
        );
      case "logs":
        return (
          <LogsPage
            events={events}
            mqttEvents={mqttEvents}
            selectedDeviceId={selectedDeviceId}
          />
        );
      case "deploy":
        return (
          <DeployPage
            devices={devices}
            preselectedDeviceId={pageArg || selectedDeviceId}
            addToast={addToast}
            refreshSnapshot={refreshSnapshot}
            refreshDetails={refreshDetails}
            working={working}
            setWorking={setWorking}
          />
        );
      default:
        return (
          <DashboardPage
            stats={stats}
            devices={devices}
            pendingDevices={pendingDevices}
            mqttEvents={mqttEvents}
            events={events}
            pkiInfo={pkiInfo}
            onBatchApprove={handleBatchApprove}
            onNavigate={navigate}
            working={working}
          />
        );
    }
  }

  /* ─── render ─── */
  return (
    <div className="app-shell">
      <ToastTray toasts={toasts} onDismiss={(id) => setToasts((cur) => cur.filter((t) => t.id !== id))} />

      {/* ─── Sidebar ─── */}
      <nav className="sidebar">
        <div className="sidebar-brand">
          <div className="brand-mark">&#x2B22;</div>
          <div className="sidebar-brand-text">
            <div className="brand-title">EdgeFlux</div>
            <div className="brand-subtitle">SecureOS</div>
          </div>
        </div>

        <div className="sidebar-nav">
          {NAV_ITEMS.map((item) => (
            <button
              key={item.id}
              className={`sidebar-nav-item ${page === item.id ? "active" : ""}`}
              onClick={() => navigate(item.id)}
            >
              <span className="sidebar-nav-icon">{item.icon}</span>
              <span className="sidebar-nav-label">{item.label}</span>
              {item.id === "devices" && pendingDevices.length > 0 && (
                <span className="sidebar-badge">{pendingDevices.length}</span>
              )}
            </button>
          ))}
        </div>

        <div className="sidebar-footer">
          <button
            className="sidebar-action"
            onClick={() => { setCreateForm(emptyCreateForm()); setCreateOpen(true); }}
            disabled={working}
          >
            + New device
          </button>
          <button className="sidebar-action" onClick={handlePurgeLegacy} disabled={working}>
            Purge legacy
          </button>
          <div className={`connection-pill ${connected ? "is-live" : ""}`}>
            <span className="connection-dot" />
            <span>{connected ? "Live" : "Reconnecting"}</span>
          </div>
        </div>
      </nav>

      {/* ─── Main content ─── */}
      <div className="main-content">
        {renderPage()}
      </div>

      {/* ─── SSH Terminal overlay ─── */}
      {sshTerminalDevice && (
        <Suspense fallback={null}>
          <SSHTerminal deviceId={sshTerminalDevice} onClose={() => setSSHTerminalDevice(null)} />
        </Suspense>
      )}

      {/* ─── Create device modal ─── */}
      {createOpen && (
        <Modal title="Create device" onClose={() => setCreateOpen(false)}>
          <form className="modal-form" onSubmit={handleCreateDevice}>
            <div className="field-grid two-up">
              <Field label="Device ID">
                <input className="text-input" value={createForm.deviceId} onChange={(e) => setCreateForm((c) => ({ ...c, deviceId: e.target.value }))} />
              </Field>
              <Field label="Profile">
                <input className="text-input" value={createForm.profile} onChange={(e) => setCreateForm((c) => ({ ...c, profile: e.target.value }))} />
              </Field>
            </div>
            <Field label="Network interfaces">
              <div className="stack-grid">
                {createForm.nics.map((nic, i) => (
                  <div key={`${nic.name}-${i}`} className="nic-grid">
                    <input className="text-input" placeholder="name" value={nic.name} onChange={(e) => updateCreateNic(i, "name", e.target.value)} />
                    <input className="text-input" placeholder="ip" value={nic.ip} onChange={(e) => updateCreateNic(i, "ip", e.target.value)} />
                    <input className="text-input" placeholder="cidr" value={nic.cidr} onChange={(e) => updateCreateNic(i, "cidr", e.target.value)} />
                    <input className="text-input" placeholder="mac" value={nic.mac} onChange={(e) => updateCreateNic(i, "mac", e.target.value)} />
                    <input className="text-input" placeholder="vlan" value={nic.vlan} onChange={(e) => updateCreateNic(i, "vlan", e.target.value)} />
                    <select className="select-input" value={nic.state} onChange={(e) => updateCreateNic(i, "state", e.target.value)}>
                      <option value="up">enabled</option>
                      <option value="down">disabled</option>
                    </select>
                    <button type="button" className="mini-button danger" onClick={() => setCreateForm((c) => ({ ...c, nics: c.nics.filter((_, j) => j !== i) }))}>Remove</button>
                  </div>
                ))}
              </div>
              <button type="button" className="mini-button" onClick={() => setCreateForm((c) => ({ ...c, nics: [...c.nics, { name: "eth0", ip: "", cidr: "24", mac: "", vlan: 0, state: "up" }] }))}>Add NIC</button>
            </Field>
            <Field label="Authorized SSH keys">
              <textarea className="text-area" value={createForm.keysText} onChange={(e) => setCreateForm((c) => ({ ...c, keysText: e.target.value }))} placeholder="comment|access|ssh-ed25519 AAAA..." />
            </Field>
            <label className="checkbox-row">
              <input type="checkbox" checked={createForm.simulate} onChange={(e) => setCreateForm((c) => ({ ...c, simulate: e.target.checked }))} />
              <span>Start local simulator after create</span>
            </label>
            <div className="modal-actions">
              <button type="button" className="mini-button" onClick={() => setCreateOpen(false)}>Cancel</button>
              <button type="submit" className="solid-button good" disabled={working}>Create device</button>
            </div>
          </form>
        </Modal>
      )}

      {/* ─── Confirm modal ─── */}
      {confirmState && (
        <Modal title={confirmState.title} onClose={() => setConfirmState(null)} tone={confirmState.tone}>
          <div className="confirm-body">{confirmState.body}</div>
          {confirmState.inputLabel && (
            <div style={{ padding: "0 20px" }}>
              <Field label={confirmState.inputLabel}>
                <input className="text-input" value={confirmState.inputValue} onChange={(e) => setConfirmState((c) => ({ ...c, inputValue: e.target.value }))} />
              </Field>
            </div>
          )}
          <div className="modal-actions" style={{ padding: "12px 20px 20px" }}>
            <button className="mini-button" onClick={() => setConfirmState(null)}>Cancel</button>
            <button
              className={confirmState.tone === "danger" ? "solid-button danger" : confirmState.tone === "warn" ? "solid-button warn" : "solid-button good"}
              onClick={submitConfirm}
            >
              Confirm
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
