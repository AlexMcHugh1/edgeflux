export function ToastTray({ toasts, onDismiss }) {
  return (
    <div className="toast-tray">
      {toasts.map((toast) => (
        <div key={toast.id} className={`toast tone-${toast.tone}`}>
          <span>{toast.message}</span>
          <button onClick={() => onDismiss(toast.id)}>×</button>
        </div>
      ))}
    </div>
  );
}

export function Modal({ title, onClose, tone = "accent", children }) {
  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className={`modal-card tone-${tone}`} onClick={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <div>{title}</div>
          <button className="mini-button" onClick={onClose}>Close</button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function Field({ label, children }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

export function InfoCard({ title, tone, children }) {
  return (
    <article className={`info-card tone-${tone}`}>
      <div className="info-title">{title}</div>
      <div className="info-body">{children}</div>
    </article>
  );
}

export function InfoRow({ label, value }) {
  return (
    <div className="info-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function DeviceMetric({ label, value }) {
  return (
    <div className="device-metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}
