import { startTransition, useDeferredValue, useState } from "react";
import { LEVELS } from "../lib/constants.js";
import { formatTime, matchesLogFilter } from "../lib/helpers.js";

export default function LogsPage({ events, mqttEvents, selectedDeviceId }) {
  const [activeTab, setActiveTab] = useState("logs");
  const [selectedOnly, setSelectedOnly] = useState(false);
  const [logFilterText, setLogFilterText] = useState("");
  const [logLevelFilter, setLogLevelFilter] = useState("all");
  const deferredLogFilter = useDeferredValue(logFilterText);

  const visibleEvents = events.filter((e) => matchesLogFilter(e, deferredLogFilter, logLevelFilter, selectedOnly, selectedDeviceId));
  const visibleMqtt = mqttEvents.filter((e) => matchesLogFilter(e, deferredLogFilter, logLevelFilter, selectedOnly, selectedDeviceId));
  const activeList = activeTab === "logs" ? visibleEvents : visibleMqtt;
  const displayed = activeTab === "logs" ? activeList.slice(-500) : activeList;

  return (
    <div className="page-content logs-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Logs</h1>
          <p className="page-subtitle">Real-time event stream and MQTT traffic</p>
        </div>
      </div>

      <section className="panel logs-panel-full">
        <div className="panel-header logs-header">
          <div className="tab-strip">
            <button className={activeTab === "logs" ? "tab-button active" : "tab-button"} onClick={() => setActiveTab("logs")}>
              Events <span>{visibleEvents.length}</span>
            </button>
            <button className={activeTab === "mqtt" ? "tab-button active" : "tab-button"} onClick={() => setActiveTab("mqtt")}>
              MQTT <span>{visibleMqtt.length}</span>
            </button>
          </div>
          <button
            className={selectedOnly ? "mini-button active" : "mini-button"}
            onClick={() => setSelectedOnly((c) => !c)}
          >
            {selectedOnly ? `Device: ${selectedDeviceId || "all"}` : "All devices"}
          </button>
        </div>
        <div className="filter-row">
          <input
            className="text-input"
            placeholder="Filter logs… plain text or /regex/"
            value={logFilterText}
            onChange={(e) => startTransition(() => setLogFilterText(e.target.value))}
          />
          <select className="select-input" value={logLevelFilter} onChange={(e) => setLogLevelFilter(e.target.value)}>
            {LEVELS.map((l) => <option key={l} value={l}>{l === "all" ? "All levels" : l}</option>)}
          </select>
        </div>
        <div className="log-stream">
          {displayed.length === 0 ? (
            <div className="empty-state">Waiting for matching events…</div>
          ) : displayed.map((ev) => (
            <article key={`${ev.id}-${ev.timestamp}`} className="log-row">
              <span className="log-time">{formatTime(ev.timestamp)}</span>
              <span className={`log-level level-${String(ev.level).toLowerCase()}`}>{ev.level}</span>
              <span className="log-source">{ev.source}</span>
              <div className="log-message">
                {ev.device_id && <span className="device-chip">{ev.device_id}</span>}
                {ev.topic && <span className="topic-chip">{ev.topic}</span>}
                <span>{ev.message}</span>
              </div>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
