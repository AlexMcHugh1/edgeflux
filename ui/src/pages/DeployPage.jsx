import { useState } from "react";
import { Field } from "../components/ui.jsx";
import { DEPLOY_PRESETS } from "../lib/constants.js";
import { apiFetch, emptyDeployForm, onlineState } from "../lib/helpers.js";

export default function DeployPage({ devices, preselectedDeviceId, addToast, refreshSnapshot, refreshDetails, working, setWorking }) {
  const [form, setForm] = useState(() => emptyDeployForm(preselectedDeviceId || ""));
  const [lastDeploy, setLastDeploy] = useState(null);

  const deviceList = Object.values(devices);
  const targetDevice = devices[form.targetId] || null;
  const targetOnline = targetDevice ? onlineState(targetDevice) : null;
  const isSimulator = targetDevice && (targetDevice.phase === "pending" || targetDevice.simulate);

  async function handleDeploy(event) {
    event.preventDefault();
    if (!form.targetId) return;
    setWorking(true);
    try {
      const env = {};
      for (const pair of form.env.split(",").map((e) => e.trim()).filter(Boolean)) {
        const [key, ...value] = pair.split("=");
        if (key && value.length > 0) env[key] = value.join("=");
      }
      const result = await apiFetch(`/api/v1/devices/${form.targetId}/containers`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: form.name.trim(),
          image: form.image.trim(),
          ports: form.ports.split(",").map((e) => e.trim()).filter(Boolean),
          env,
          read_only_rootfs: form.readOnlyRootfs,
          no_new_privileges: form.noNewPrivileges,
          seccomp_profile: "runtime/default",
        }),
      });
      addToast(`Container ${form.name} queued for ${form.targetId}`, "good");
      setLastDeploy({ device: form.targetId, container: form.name, time: new Date().toLocaleTimeString() });
      await refreshSnapshot();
      await refreshDetails(form.targetId);
    } catch (error) {
      addToast(error.message, "danger");
    } finally {
      setWorking(false);
    }
  }

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Deploy container</h1>
          <p className="page-subtitle">Deploy containers to any device including simulators</p>
        </div>
      </div>

      <div className="deploy-layout">
        <form className="panel deploy-form-panel" onSubmit={handleDeploy}>
          <div className="panel-header">
            <div className="panel-title">Container configuration</div>
          </div>

          <div className="deploy-form-body">
            <Field label="Target device">
              <select
                className="select-input"
                value={form.targetId}
                onChange={(e) => setForm((c) => ({ ...c, targetId: e.target.value }))}
              >
                <option value="">Select a device…</option>
                {deviceList.map((d) => {
                  const st = onlineState(d);
                  return (
                    <option key={d.device_id} value={d.device_id}>
                      {d.device_id} ({st.label} · {d.approval_status || d.status})
                    </option>
                  );
                })}
              </select>
            </Field>

            {targetDevice && (
              <div className="deploy-target-info">
                <span className={`status-dot tone-${targetOnline.tone}`} />
                <span className="deploy-target-id">{targetDevice.device_id}</span>
                <span className={`approval-pill tone-${targetOnline.tone}`}>{targetOnline.label}</span>
                {isSimulator && <span className="approval-pill tone-cyan">simulator</span>}
                <span className="deploy-target-phase">{targetDevice.phase || "unknown"}</span>
              </div>
            )}

            <Field label="Quick presets">
              <div className="preset-row">
                {Object.entries(DEPLOY_PRESETS).map(([key, preset]) => (
                  <button
                    key={key}
                    type="button"
                    className={`preset-card ${form.name === preset.name ? "active" : ""}`}
                    onClick={() => setForm((c) => ({ ...c, ...preset }))}
                  >
                    <span>{preset.name}</span>
                    <small>{preset.image}</small>
                  </button>
                ))}
              </div>
            </Field>

            <div className="field-grid two-up">
              <Field label="Container name">
                <input className="text-input" value={form.name} onChange={(e) => setForm((c) => ({ ...c, name: e.target.value }))} />
              </Field>
              <Field label="Image">
                <input className="text-input" value={form.image} onChange={(e) => setForm((c) => ({ ...c, image: e.target.value }))} />
              </Field>
            </div>

            <div className="field-grid two-up">
              <Field label="Ports">
                <input className="text-input" value={form.ports} onChange={(e) => setForm((c) => ({ ...c, ports: e.target.value }))} placeholder="8080/tcp,8443/tcp" />
              </Field>
              <Field label="Environment">
                <input className="text-input" value={form.env} onChange={(e) => setForm((c) => ({ ...c, env: e.target.value }))} placeholder="MODE=demo,LOG_LEVEL=info" />
              </Field>
            </div>

            <div className="checkbox-stack">
              <label className="checkbox-row">
                <input type="checkbox" checked={form.readOnlyRootfs} onChange={(e) => setForm((c) => ({ ...c, readOnlyRootfs: e.target.checked }))} />
                <span>Read-only rootfs</span>
              </label>
              <label className="checkbox-row">
                <input type="checkbox" checked={form.noNewPrivileges} onChange={(e) => setForm((c) => ({ ...c, noNewPrivileges: e.target.checked }))} />
                <span>No new privileges</span>
              </label>
            </div>

            <div className="deploy-submit-row">
              <button type="submit" className="solid-button good deploy-btn" disabled={working || !form.targetId}>
                Deploy to {form.targetId || "…"}
              </button>
            </div>
          </div>
        </form>

        <div className="deploy-sidebar">
          <section className="panel">
            <div className="panel-header">
              <div className="panel-title">Available targets</div>
            </div>
            <div className="deploy-device-list">
              {deviceList.length === 0 && <div className="empty-inline">No devices registered</div>}
              {deviceList.map((d) => {
                const st = onlineState(d);
                const isActive = form.targetId === d.device_id;
                return (
                  <button
                    key={d.device_id}
                    className={`device-list-item ${isActive ? "selected" : ""}`}
                    type="button"
                    onClick={() => setForm((c) => ({ ...c, targetId: d.device_id }))}
                  >
                    <span className={`status-dot tone-${st.tone}`} />
                    <div className="device-list-item-info">
                      <span className="device-list-item-id">{d.device_id}</span>
                      <span className="device-list-item-meta">{d.phase || "unknown"} · {st.label}</span>
                    </div>
                    <span className={`approval-pill tone-${st.tone}`}>{st.label}</span>
                  </button>
                );
              })}
            </div>
          </section>

          {lastDeploy && (
            <section className="panel">
              <div className="panel-header">
                <div className="panel-title">Last deployment</div>
              </div>
              <div style={{ padding: "0 20px 16px" }}>
                <div className="info-row"><span>Device</span><strong>{lastDeploy.device}</strong></div>
                <div className="info-row"><span>Container</span><strong>{lastDeploy.container}</strong></div>
                <div className="info-row"><span>Time</span><strong>{lastDeploy.time}</strong></div>
              </div>
            </section>
          )}
        </div>
      </div>
    </div>
  );
}
