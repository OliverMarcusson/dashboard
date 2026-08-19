import { useEffect, useMemo, useRef, useState } from 'react';
import { Pause, Play, Trash2 } from 'lucide-react';
import { Card, Empty } from '../components/ui';
import type { Services } from './Stacks';

type Line = { text: string; stream: string; at?: string };

export function Logs({ services, units }: { services?: Services; units?: { units: { name: string }[] } }) {
  const sources = useMemo(() => {
    const containers = (services?.stacks ?? []).flatMap((s) =>
      s.containers.map((c) => ({ kind: 'container' as const, target: c.name, group: s.name })),
    );
    const svc = (units?.units ?? []).map((u) => ({
      kind: 'unit' as const,
      target: u.name,
      group: 'systemd',
    }));
    return [...containers, ...svc];
  }, [services, units]);

  const [selected, setSelected] = useState('');
  const [filter, setFilter] = useState('');
  const [paused, setPaused] = useState(false);
  const [lines, setLines] = useState<Line[]>([]);
  const boxRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  // Default to the first container once discovery has produced one.
  useEffect(() => {
    if (!selected && sources.length) setSelected(`${sources[0].kind}:${sources[0].target}`);
  }, [sources, selected]);

  useEffect(() => {
    if (!selected) return;
    const [kind, target] = selected.split(':');
    setLines([]);

    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const socket = new WebSocket(
      `${scheme}://${location.host}/ws/logs?kind=${kind}&target=${encodeURIComponent(target)}&tail=300`,
    );

    socket.onmessage = (e) => {
      if (pausedRef.current) return;
      try {
        const line = JSON.parse(e.data) as Line;
        // Keep the buffer bounded: a chatty container should not grow the tab
        // until it is killed.
        setLines((prev) => (prev.length > 2000 ? [...prev.slice(-1500), line] : [...prev, line]));
      } catch {
        /* ignore malformed frames */
      }
    };

    return () => socket.close();
  }, [selected]);

  useEffect(() => {
    if (!paused && boxRef.current) boxRef.current.scrollTop = boxRef.current.scrollHeight;
  }, [lines, paused]);

  const shown = filter
    ? lines.filter((l) => l.text.toLowerCase().includes(filter.toLowerCase()))
    : lines;

  if (!sources.length) return <Empty>Waiting for discovery…</Empty>;

  return (
    <Card
      title={
        <select
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          style={{
            font: 'inherit',
            background: 'var(--surface)',
            color: 'var(--text)',
            border: '1px solid var(--line)',
            borderRadius: 6,
            padding: '0.25rem 0.5rem',
            maxWidth: '16rem',
          }}
        >
          {sources.map((s) => (
            <option key={`${s.kind}:${s.target}`} value={`${s.kind}:${s.target}`}>
              {s.group} / {s.target}
            </option>
          ))}
        </select>
      }
      actions={
        <>
          <input
            placeholder="Filter"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            style={{ width: '9rem', padding: '0.3rem 0.5rem', fontSize: '0.85rem' }}
          />
          <button
            className="icon-btn"
            onClick={() => setPaused((p) => !p)}
            title={paused ? 'Resume' : 'Pause'}
            aria-label={paused ? 'Resume' : 'Pause'}
          >
            {paused ? <Play size={15} /> : <Pause size={15} />}
          </button>
          <button className="icon-btn" onClick={() => setLines([])} title="Clear" aria-label="Clear">
            <Trash2 size={15} />
          </button>
        </>
      }
      bodyless
    >
      <div className="logbox" ref={boxRef}>
        {shown.length === 0 ? (
          <div className="empty">{lines.length ? 'Nothing matches that filter.' : 'Waiting for output…'}</div>
        ) : (
          shown.map((l, i) => (
            <div key={i} className={`logline ${l.stream}`}>
              {l.text}
            </div>
          ))
        )}
      </div>
    </Card>
  );
}
