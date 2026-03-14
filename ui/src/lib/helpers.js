export async function apiFetch(path, init) {
  const response = await fetch(path, init);
  const body = await response.json().catch(() => ({}));
  if (!response.ok || body?.error) {
    throw new Error(body?.error || `Request failed (${response.status})`);
  }
  return body;
}

export function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleTimeString();
}

export function formatDateTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

export function formatTTL(value) {
  if (!value) return "-";
  const date = new Date(value);
  const delta = date.getTime() - Date.now();
  if (Number.isNaN(delta)) return "-";
  if (delta <= 0) return "expired";
  const hours = Math.floor(delta / 3600000);
  const minutes = Math.floor((delta % 3600000) / 60000);
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

export function onlineState(device) {
  if (device?.connection_alive) return { label: "online", tone: "good" };
  if (!device?.last_health) return { label: "offline", tone: "warn" };
  const delta = Date.now() - new Date(device.last_health).getTime();
  if (delta <= 15000) return { label: "grace", tone: "accent" };
  return { label: "offline", tone: "warn" };
}

export function approvalTone(device) {
  if (device?.approval_status === "revoked") return "danger";
  if (device?.approval_status === "pending" || device?.status === "pending_approval") return "warn";
  return "good";
}

export function canApprove(device) {
  return device?.approval_status === "pending";
}

export function canRevoke(device) {
  return device?.approval_status === "approved";
}

export function canReboot(device) {
  return device?.approval_status === "approved" && (device?.connection_alive || device?.cert_serial);
}

export function canStartAgent(device) {
  return device?.approval_status !== "revoked" && !device?.connection_alive;
}

export function matchesLogFilter(event, filterText, levelFilter, selectedOnly, selectedId) {
  if (selectedOnly && selectedId && event.device_id !== selectedId) return false;
  if (levelFilter !== "all" && event.level !== levelFilter) return false;
  if (!filterText) return true;
  const haystack = `${event.timestamp || ""} ${event.level || ""} ${event.source || ""} ${event.device_id || ""} ${event.message || ""} ${event.topic || ""}`.toLowerCase();
  if (filterText.startsWith("/") && filterText.endsWith("/") && filterText.length > 2) {
    try {
      return new RegExp(filterText.slice(1, -1), "i").test(haystack);
    } catch {
      return false;
    }
  }
  return haystack.includes(filterText.toLowerCase());
}

export function matchesDeviceFilter(device, filterValue) {
  if (filterValue === "all") return true;
  if (filterValue === "online") return onlineState(device).label !== "offline";
  if (filterValue === "offline") return onlineState(device).label === "offline";
  if (filterValue === "pending") return device?.approval_status === "pending" || device?.status === "pending_approval";
  if (filterValue === "revoked") return device?.approval_status === "revoked";
  return true;
}

export function sortDevices(devices, sortKey) {
  const copy = [...devices];
  copy.sort((left, right) => {
    if (sortKey === "health") return (right.health_messages || 0) - (left.health_messages || 0);
    if (sortKey === "phase") return (left.phase || "").localeCompare(right.phase || "");
    if (sortKey === "last_seen") return new Date(right.last_seen || 0).getTime() - new Date(left.last_seen || 0).getTime();
    return (left.device_id || "").localeCompare(right.device_id || "");
  });
  return copy;
}

export function parseKeys(text) {
  return text
    .split(/\n|;/)
    .map((row) => row.trim())
    .filter(Boolean)
    .map((row) => {
      const parts = row.split("|").map((part) => part.trim());
      return { comment: parts[0] || "operator", access_level: parts[1] || "root", public_key: parts[2] || "" };
    })
    .filter((entry) => entry.public_key);
}

export function normalizeDevice(device) {
  return {
    ...device,
    containers: device?.containers || {},
    nics: device?.nics || [],
    authorized_keys: device?.authorized_keys || [],
  };
}

export function deviceMapFromList(list) {
  const next = {};
  for (const device of list || []) {
    next[device.device_id] = normalizeDevice(device);
  }
  return next;
}

export function pushLimited(list, item, limit, toFront = false) {
  const next = toFront ? [item, ...list] : [...list, item];
  if (next.length <= limit) return next;
  return toFront ? next.slice(0, limit) : next.slice(next.length - limit);
}

export const emptyCreateForm = () => ({
  deviceId: `edge-${Math.random().toString(36).slice(2, 8)}`,
  profile: "alpine-edge-secure",
  simulate: true,
  keysText: "",
  nics: [
    { name: "eth0", ip: "10.20.1.5", cidr: "24", mac: "02:42:ac:11:00:02", vlan: 10, state: "up" },
    { name: "wlan0", ip: "192.168.50.2", cidr: "24", mac: "02:42:ac:11:00:03", vlan: 0, state: "down" },
  ],
});

export const emptyDeployForm = (targetId = "") => ({
  targetId,
  name: "nginx-edge",
  image: "nginx:alpine",
  ports: "8080/tcp",
  env: "",
  readOnlyRootfs: true,
  noNewPrivileges: true,
});
