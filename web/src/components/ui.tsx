import { useEffect, useRef, type ReactNode } from 'react';

export function Card({
  title,
  actions,
  children,
  bodyless,
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  bodyless?: boolean;
}) {
  return (
    <section className="card">
      {title && (
        <header className="card-head">
          <h2>{title}</h2>
          <span className="spacer" />
          {actions}
        </header>
      )}
      {bodyless ? children : <div className="card-body">{children}</div>}
    </section>
  );
}

type Tone = 'up' | 'down' | 'warn' | 'bad';

export function Status({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span className={`status ${tone}`}>
      <span className="dot" />
      {children}
    </span>
  );
}

export function Bar({ percent }: { percent: number }) {
  const clamped = Math.max(0, Math.min(100, percent));
  const tone = clamped >= 90 ? 'crit' : clamped >= 75 ? 'warn' : '';
  return (
    <div className={`bar ${tone}`} role="presentation">
      <span style={{ width: `${clamped}%` }} />
    </div>
  );
}

export function Stat({
  label,
  value,
  sub,
  percent,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  percent?: number;
}) {
  return (
    <div className="stat">
      <span className="label">{label}</span>
      <span className="value">{value}</span>
      {percent !== undefined && <Bar percent={percent} />}
      {sub && <span className="sub">{sub}</span>}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

/**
 * The confirmation step every action passes through. There are no danger
 * tiers: restarting a Minecraft server and stopping Docker ask the same way.
 */
export function Confirm({
  question,
  detail,
  busy,
  onConfirm,
  onCancel,
}: {
  question: string;
  detail?: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    confirmRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel();
    };
    addEventListener('keydown', onKey);
    return () => removeEventListener('keydown', onKey);
  }, [onCancel]);

  return (
    <div className="backdrop" onClick={(e) => e.target === e.currentTarget && onCancel()}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={question}>
        <h3>{question}</h3>
        {detail && <p>{detail}</p>}
        <div className="buttons">
          <button onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button ref={confirmRef} className="btn-primary" onClick={onConfirm} disabled={busy}>
            {busy ? 'Running…' : 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  );
}
