import { startTransition, useDeferredValue, useState } from "react";
import { InfoCard, InfoRow, DeviceMetric } from "../components/ui.jsx";
import { DEVICE_FILTERS, DEVICE_SORTS } from "../lib/constants.js";
import {
  formatTime, formatDateTime, formatTTL, onlineState, approvalTone,
  canApprove, canRevoke, canReboot, canStartAgent,
  matchesDeviceFilter, sortDevices,
} from "../lib/helpers.js";

export default function DevicesPage({
  devices, selectedDeviceId, setSelectedDeviceId,
  details, detailLoading, pkiInfo, working,
  onControl, onSimulator, onRemoveSimulator, onRemoveContainer,
  onRequestManifest, onOpenDeploy, onNavigate,
  onSSHConnect, onSSHKeys, onStopSSH, onOpenTerminal,
}) {
  const [deviceQuery, setDeviceQuery] = useState("");
  const [deviceFilter, setDeviceFilter] = useState("all");
  const [deviceSort, setDeviceSort] = useState("device_id");
  const deferredQuery = useDeferredValue(deviceQuery);

  const selectedDevice = devices[selectedDeviceId] || null;

  const visibleDevices = sortDevices(
    Object.values(devices)
      .filter((d) => matchesDeviceFilter(d, deviceFilter))
      .filter((d) => {
        if (!deferredQuery.trim()) return true;
        const haystack = `${d.device_id} ${d.phase || ""} ${d.status || ""} ${d.approval_status || ""}`.toLowerCase();
        return haystack.includes(deferredQuery.toLowerCase());
      }),
    deviceSort,
  );

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Devices</h1>
          <p className="page-subtitle">{Object.keys(devices).length} registered devices</p>
        </div>
      </div>

      <div className="devices-layout">
        {/* Left: Device list */}
        <section className="panel device-list-panel">
          <div className="panel-header">
            <div className="panel-title">Device explorer</div>
          </div>
          <div className="explorer-controls">
            <input
              className="text-input"
              placeholder="Search device id, phase, or status…"
              value={deviceQuery}
              onChange={(e) => startTransition(() => setDeviceQuery(e.target.value))}
            />
            <select className="select-input" value={deviceSort} onChange={(e) => setDeviceSort(e.target.value)}>
              {DEVICE_SORTS.map((s) => <option key={s.value} value={s.value}>Sort: {s.label}</option>)}
            </select>
          </div>
          <div className="pill-row">
            {DEVICE_FILTERS.map((f) => (
              <button key={f} className={deviceFilter === f ? "mini-button active" : "mini-button"} onClick={() => setDeviceFilter(f)}>
                {f}
              </button>
            ))}
          </div>
          <div className="device-list-scroll">
            {visibleDevices.length === 0 && <div className="empty-state compact">No devices match filters.</div>}
            {visibleDevices.map((device) => {
              const online = onlineState(device);
              const isSelected = selectedDeviceId === device.device_id;
              return (
                <button
                  key={device.device_id}
                  className={`device-list-item ${isSelected ? "selected" : ""}`}
                  onClick={() => setSelectedDeviceId(device.device_id)}
                >
                  <span className={`status-dot tone-${online.tone}`} />
                  <div className="device-list-item-info">
                    <span className="device-list-item-id">{device.device_id}</span>
                    <span className="device-list-item-meta">{device.phase || "unknown"} · {online.label}</span>
                  </div>
                  <span className={`approval-pill tone-${approvalTone(device)}`}>
                    {device.approval_status || device.status || "unknown"}
                  </span>
                </button>
              );
            })}
          </div>
        </section>

        {/* Right: Device detail */}
        <section className="panel device-detail-panel">
          {!selectedDevice ? (
            <div className="empty-state">Select a device to inspect its state.</div>
          ) : (
            <DeviceDetail
              device={selectedDevice}
              details={details}
              detailLoading={detailLoading}
              pkiInfo={pkiInfo}
              working={working}
              onControl={onControl}
              onSimulator={onSimulator}
              onRemoveSimulator={onRemoveSimulator}
              onRemoveContainer={onRemoveContainer}
              onRequestManifest={onRequestManifest}
              onOpenDeploy={onOpenDeploy}
              onNavigate={onNavigate}
              onSSHConnect={onSSHConnect}
              onSSHKeys={onSSHKeys}
              onStopSSH={onStopSSH}
              onOpenTerminal={onOpenTerminal}
            />
          )}
        </section>
      </div>
    </div>
  );
}

function DeviceDetail({ device, details, detailLoading, pkiInfo, working, onControl, onSimulator, onRemoveSimulator, onRemoveContainer, onRequestManifest, onOpenDeploy, onNavigate, onSSHConnect, onSSHKeys, onStopSSH, onOpenTerminal }) {
  const online = onlineState(device);

  return (
    <div className="device-detail-scroll">
      <div className="panel-header">
        <div>
          <div className="panel-title">{device.device_id}</div>
          <div className="panel-copy">Lifecycle controls, runtime metadata, and rollout commands</div>
        </div>
        {detailLoading && <span className="detail-loading">Refreshing</span>}
      </div>

      <div className="detail-hero">
        <div className="detail-meta">
          <span className={`approval-pill tone-${approvalTone(device)}`}>{device.approval_status || device.status}</span>
          <span className={`approval-pill tone-${online.tone}`}>{online.label}</span>
          <span className="approval-pill tone-cyan">phase {device.phase || "unknown"}</span>
        </div>
        <div className="action-row">
          {canStartAgent(device) && <button className="mini-button" onClick={() => onSimulator(device.device_id)} disabled={working}>Start agent</button>}
          {canApprove(device) && <button className="solid-button good" onClick={() => onControl(device.device_id, "approve")} disabled={working}>Approve</button>}
          {canReboot(device) && <button className="mini-button warn" onClick={() => onControl(device.device_id, "reboot")} disabled={working}>Reboot</button>}
          {canRevoke(device) && <button className="mini-button danger" onClick={() => onControl(device.device_id, "revoke")} disabled={working}>Revoke</button>}
        </div>
      </div>

      <div className="detail-grid two-up">
        <InfoCard title="Identity" tone="cyan">
          <InfoRow label="Device ID" value={device.device_id} />
          <InfoRow label="Certificate serial" value={device.cert_serial || "-"} />
          <InfoRow label="Thumbprint" value={device.cert_thumbprint || "-"} />
          <InfoRow label="Cert expiry" value={device.cert_not_after ? `${formatDateTime(device.cert_not_after)} (${formatTTL(device.cert_not_after)})` : "-"} />
        </InfoCard>
        <InfoCard title="Runtime" tone="accent">
          <InfoRow label="Health messages" value={device.health_messages || 0} />
          <InfoRow label="MQTT messages" value={device.mqtt_messages || 0} />
          <InfoRow label="SSH tunnel" value={device.ssh_tunnel || "-"} />
          <InfoRow label="Last seen" value={formatDateTime(device.last_seen)} />
        </InfoCard>
      </div>

      <div className="action-row wrap">
        <button className="mini-button" onClick={() => onRequestManifest("os")} disabled={working}>Send OS manifest</button>
        <button className="mini-button" onClick={() => onRequestManifest("ssh")} disabled={working}>Send SSH config</button>
        <button className="mini-button" onClick={() => onNavigate("deploy", device.device_id)} disabled={working}>Deploy container</button>
        {details.simulate?.exec_cmd && (
          <button className="mini-button" onClick={() => navigator.clipboard.writeText(details.simulate.exec_cmd)}>
            Copy exec command
          </button>
        )}
        {details.simulate && <button className="mini-button danger" onClick={() => onRemoveSimulator(device.device_id)} disabled={working}>Remove simulator</button>}
      </div>

      <div className="detail-grid two-up">
        <InfoCard title="Network interfaces" tone="amber">
          {device.nics.length === 0 && <div className="empty-inline">No NIC metadata</div>}
          {device.nics.map((nic) => (
            <div key={`${nic.name}-${nic.mac}`} className="subcard">
              <div className="subcard-top">
                <span>{nic.name}</span>
                <span className={nic.state === "down" ? "approval-pill tone-danger" : "approval-pill tone-good"}>{nic.state || "up"}</span>
              </div>
              <div className="subcard-copy">{nic.ip || "-"}{nic.cidr ? `/${nic.cidr}` : ""}</div>
              <div className="subcard-copy">{nic.mac || "-"} {Number.isFinite(nic.vlan) ? `• vlan ${nic.vlan}` : ""}</div>
            </div>
          ))}
        </InfoCard>

        <InfoCard title="Authorized keys" tone="good">
          {device.authorized_keys.length === 0 && <div className="empty-inline">Default policy</div>}
          {device.authorized_keys.map((key, i) => (
            <InfoRow key={`${key.public_key}-${i}`} label={key.comment || "operator"} value={`${key.access_level || "user"} • ${String(key.public_key || "").slice(0, 32)}…`} />
          ))}          <div className="action-row compact" style={{ marginTop: 8 }}>
            <button className="mini-button" onClick={() => onSSHKeys(device.device_id)} disabled={working}>Generate SSH key</button>
          </div>        </InfoCard>
      </div>

      <div className="detail-grid">
        <InfoCard title="Containers" tone="cyan">
          {Object.keys(details.containers?.containers || {}).length === 0 && <div className="empty-inline">No containers reported</div>}
          {Object.entries(details.containers?.containers || {}).map(([name, state]) => (
            <div key={name} className="subcard">
              <div className="subcard-top">
                <span>{name}</span>
                <span className={`approval-pill tone-${state === "running" ? "good" : state === "deployed" ? "amber" : state === "failed" ? "danger" : state === "stopped" || state === "exited" ? "danger" : "cyan"}`}>{state}</span>
              </div>
              <div className="subcard-copy">{details.containers?.specs?.[name]?.image || "No image metadata"}</div>
              <div className="action-row compact">
                <button className="mini-button danger" onClick={() => onRemoveContainer(device.device_id, name)} disabled={working}>Remove</button>
              </div>
            </div>
          ))}
          <button className="mini-button" onClick={() => onNavigate("deploy", device.device_id)} style={{ marginTop: 8 }}>
            Deploy new container
          </button>
        </InfoCard>
      </div>

      <div className="detail-grid two-up">
        <InfoCard title="SSH access" tone="accent">
          {details.sshProxy?.running ? (
            <div>
              <InfoRow label="Status" value="running" />
              <InfoRow label="SSH port" value={details.sshProxy.host_port || "-"} />
              <InfoRow label="Username" value="edge" />
              <div className="action-row compact" style={{ marginTop: 8 }}>
                <button className="solid-button good" onClick={() => onOpenTerminal(device.device_id)} disabled={working}>Open terminal</button>
                <button className="mini-button" onClick={() => onSSHKeys(device.device_id)} disabled={working}>Download key</button>
                <button className="mini-button" onClick={() => navigator.clipboard.writeText(`ssh -o StrictHostKeyChecking=no -i edgeflux_${device.device_id}_key edge@localhost -p ${details.sshProxy.host_port}`)}>Copy SSH cmd</button>
                <button className="mini-button danger" onClick={() => onStopSSH(device.device_id)} disabled={working}>Stop</button>
              </div>
            </div>
          ) : (
            <div>
              <InfoRow label="Status" value="not running" />
              <InfoRow label="SSH tunnel" value={device.ssh_tunnel || "-"} />
              <div className="action-row compact" style={{ marginTop: 8 }}>
                <button className="solid-button good" onClick={() => onSSHConnect(device.device_id)} disabled={working}>
                  Start SSH proxy
                </button>
              </div>
            </div>
          )}
        </InfoCard>

        <InfoCard title="Simulator & vault" tone="amber">
          <InfoRow label="Simulator" value={details.simulate?.container_status || details.simulate?.status || "not_found"} />
          <InfoRow label="Image" value={details.simulate?.image || "-"} />
          <InfoRow label="Vault serial" value={details.vault?.cert_serial || "-"} />
          <InfoRow label="Vault expiry" value={details.vault?.not_after || "-"} />
        </InfoCard>
      </div>

      <div className="detail-grid">
        <InfoCard title="Recent device events" tone="good">
          <div className="timeline">
            {details.events.length === 0 && <div className="empty-inline">No recent events</div>}
            {details.events.map((ev) => (
              <div key={`${ev.id}-${ev.timestamp}`} className="timeline-row">
                <span className="timeline-time">{formatTime(ev.timestamp)}</span>
                <span className={`log-level level-${String(ev.level).toLowerCase()}`}>{ev.level}</span>
                <span>{ev.message}</span>
              </div>
            ))}
          </div>
        </InfoCard>
      </div>

      <div className="detail-grid two-up">
        <InfoCard title="PKI root" tone="cyan">
          <InfoRow label="Serial" value={pkiInfo.root_ca?.serial || "-"} />
          <InfoRow label="Thumbprint" value={pkiInfo.root_ca?.thumbprint || "-"} />
          <InfoRow label="Expires" value={pkiInfo.root_ca?.not_after || "-"} />
        </InfoCard>
        <InfoCard title="PKI intermediate" tone="accent">
          <InfoRow label="Serial" value={pkiInfo.intermediate_ca?.serial || "-"} />
          <InfoRow label="Issuer" value={pkiInfo.intermediate_ca?.issuer || "-"} />
          <InfoRow label="Thumbprint" value={pkiInfo.intermediate_ca?.thumbprint || "-"} />
        </InfoCard>
      </div>
    </div>
  );
}
