import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

export default function SSHTerminal({ deviceId, onClose }) {
  const containerRef = useRef(null);
  const termRef = useRef(null);
  const wsRef = useRef(null);

  useEffect(() => {
    if (!deviceId || !containerRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
      theme: {
        background: "#0d1117",
        foreground: "#c9d1d9",
        cursor: "#58a6ff",
        selectionBackground: "#264f78",
      },
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(containerRef.current);
    fitAddon.fit();
    termRef.current = term;

    term.writeln("\x1b[36mConnecting to " + deviceId + "...\x1b[0m");

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${proto}//${window.location.host}/api/v1/ssh-terminal/${encodeURIComponent(deviceId)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      term.writeln("\x1b[32mConnected.\x1b[0m\r\n");
    };

    ws.onmessage = (ev) => {
      const data = ev.data instanceof ArrayBuffer
        ? new TextDecoder().decode(ev.data)
        : ev.data;
      term.write(data);
    };

    ws.onerror = () => {
      term.writeln("\r\n\x1b[31mWebSocket error.\x1b[0m");
    };

    ws.onclose = (ev) => {
      term.writeln(`\r\n\x1b[33mSession ended (${ev.code}).\x1b[0m`);
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    const onResize = () => {
      fitAddon.fit();
    };
    window.addEventListener("resize", onResize);

    return () => {
      window.removeEventListener("resize", onResize);
      ws.close();
      term.dispose();
    };
  }, [deviceId]);

  return (
    <div className="ssh-terminal-overlay">
      <div className="ssh-terminal-window">
        <div className="ssh-terminal-header">
          <span className="ssh-terminal-title">SSH: {deviceId}</span>
          <button className="mini-button danger" onClick={onClose}>Close</button>
        </div>
        <div className="ssh-terminal-body" ref={containerRef} />
      </div>
    </div>
  );
}
