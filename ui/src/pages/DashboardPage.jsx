import { InfoCard, InfoRow } from "../components/ui.jsx";
import { canApprove, formatDateTime, formatTTL, onlineState } from "../lib/helpers.js";

export default function DashboardPage({ stats, devices, pendingDevices, mqttEvents, events, pkiInfo, onBatchApprove, onNavigate, working }) {
  const deviceList = Object.values(devices);
  const summaryCards = [
    { label: "Devices", value: stats.total_devices || deviceList.length, tone: "accent" },
    { label: "Pending", value: stats.pending || pendingDevices.length, tone: "warn" },
    { label: "Enrolled", value: stats.enrolled || deviceList.filter((d) => d.cert_serial).length, tone: "good" },
    { label: "Operational", value: stats.operational || deviceList.filter((d) => d.phase === "complete" || d.phase === "ssh").length, tone: "good" },
    { label: "Connected", value: stats.connected || deviceList.filter((d) => d.connection_alive).length, tone: "accent" },
    { label: "Revoked", value: stats.revoked || deviceList.filter((d) => d.approval_status === "revoked").length, tone: "danger" },
    { label: "MQTT msgs", value: stats.total_mqtt || mqttEvents.length, tone: "cyan" },
    { label: "Total events", value: stats.total_events || events.length, tone: "amber" },
  ];

  const onlineCount = deviceList.filter((d) => onlineState(d).label !== "offline").length;
  const offlineCount = deviceList.length - onlineCount;

  return (
    <div className="page-content">
      <div className="page-header">
        <div>
          <h1 className="page-title">Dashboard</h1>
          <p className="page-subtitle">Fleet overview and enrollment status</p>
        </div>
      </div>

      {pendingDevices.length > 0 && (
        <section className="pending-banner">
          <div>
            <div className="pending-title">Devices awaiting approval</div>
            <div className="pending-copy">{pendingDevices.map((d) => d.device_id).join(", ")}</div>
          </div>
          <div className="pending-actions">
            <span className="pending-count">{pendingDevices.length}</span>
            <button className="solid-button good" onClick={onBatchApprove} disabled={working}>Approve all</button>
          </div>
        </section>
      )}

      <section className="summary-grid">
        {summaryCards.map((card) => (
          <article key={card.label} className={`summary-card tone-${card.tone}`}>
            <div className="summary-label">{card.label}</div>
            <div className={`summary-value tone-${card.tone}`}>{card.value}</div>
          </article>
        ))}
      </section>

      <div className="dashboard-panels">
        <section className="panel">
          <div className="panel-header">
            <div>
              <div className="panel-title">Fleet health</div>
              <div className="panel-copy">Device connectivity overview</div>
            </div>
          </div>
          <div className="dashboard-health-grid">
            <div className="health-stat">
              <span className="health-stat-value tone-good">{onlineCount}</span>
              <span className="health-stat-label">Online</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-value tone-warn">{offlineCount}</span>
              <span className="health-stat-label">Offline</span>
            </div>
            <div className="health-stat">
              <span className="health-stat-value tone-danger">{summaryCards[5].value}</span>
              <span className="health-stat-label">Revoked</span>
            </div>
          </div>

          {deviceList.length > 0 && (
            <div className="dashboard-device-list">
              {deviceList.slice(0, 8).map((device) => {
                const online = onlineState(device);
                return (
                  <button key={device.device_id} className="dashboard-device-row" onClick={() => { onNavigate("devices", device.device_id); }}>
                    <span className={`status-dot tone-${online.tone}`} />
                    <span className="dashboard-device-id">{device.device_id}</span>
                    <span className={`approval-pill tone-${online.tone}`}>{online.label}</span>
                    <span className="dashboard-device-phase">{device.phase || "unknown"}</span>
                  </button>
                );
              })}
              {deviceList.length > 8 && (
                <button className="mini-button" onClick={() => onNavigate("devices")} style={{ marginTop: 8 }}>
                  View all {deviceList.length} devices
                </button>
              )}
            </div>
          )}
        </section>

        <div className="dashboard-right-col">
          <InfoCard title="PKI Root CA" tone="cyan">
            <InfoRow label="Serial" value={pkiInfo.root_ca?.serial || "-"} />
            <InfoRow label="Thumbprint" value={pkiInfo.root_ca?.thumbprint || "-"} />
            <InfoRow label="Expires" value={pkiInfo.root_ca?.not_after ? `${formatDateTime(pkiInfo.root_ca.not_after)} (${formatTTL(pkiInfo.root_ca.not_after)})` : "-"} />
          </InfoCard>
          <InfoCard title="PKI Intermediate CA" tone="accent">
            <InfoRow label="Serial" value={pkiInfo.intermediate_ca?.serial || "-"} />
            <InfoRow label="Issuer" value={pkiInfo.intermediate_ca?.issuer || "-"} />
            <InfoRow label="Thumbprint" value={pkiInfo.intermediate_ca?.thumbprint || "-"} />
          </InfoCard>

          <div className="dashboard-quick-actions">
            <div className="panel-title" style={{ marginBottom: 10 }}>Quick actions</div>
            <button className="ghost-button full-width" onClick={() => onNavigate("devices")}>Browse devices</button>
            <button className="ghost-button full-width" onClick={() => onNavigate("logs")}>View logs</button>
            <button className="ghost-button full-width" onClick={() => onNavigate("deploy")}>Deploy container</button>
          </div>
        </div>
      </div>
    </div>
  );
}
