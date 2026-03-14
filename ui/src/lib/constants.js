export const LEVELS = ["all", "ERROR", "WARN", "INFO", "OK", "MQTT", "TLS"];

export const DEVICE_FILTERS = ["all", "online", "offline", "pending", "revoked"];

export const DEVICE_SORTS = [
  { value: "device_id", label: "Device ID" },
  { value: "last_seen", label: "Last Seen" },
  { value: "health", label: "Health" },
  { value: "phase", label: "Phase" },
];

export const DEPLOY_PRESETS = {
  "hello-world": { name: "hello-world", image: "hello-world:latest", ports: "", env: "" },
  busybox: { name: "busybox-demo", image: "busybox:1.36", ports: "", env: "MODE=demo" },
  nginx: { name: "nginx-edge", image: "nginx:alpine", ports: "8080/tcp", env: "" },
  redis: { name: "redis-edge", image: "redis:7-alpine", ports: "6379/tcp", env: "" },
  "http-echo": { name: "http-echo", image: "hashicorp/http-echo:1.0", ports: "5678/tcp", env: "TEXT=edgeflux" },
  alpine: { name: "alpine-base", image: "alpine:3.20", ports: "", env: "MODE=debug" },
};

export const NAV_ITEMS = [
  { id: "dashboard", label: "Dashboard", icon: "◫" },
  { id: "devices", label: "Devices", icon: "⊞" },
  { id: "logs", label: "Logs", icon: "☰" },
  { id: "deploy", label: "Deploy", icon: "▶" },
];
