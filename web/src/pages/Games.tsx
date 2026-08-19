import { useState } from 'react';
import { Terminal } from 'lucide-react';
import { api } from '../api';
import { Card, Empty, Status } from '../components/ui';

export type GameServer = {
  container: string;
  adapter: string;
  game: string;
  edition?: string;
  version?: string;
  network: string;
  proxy: boolean;
  address?: string;
  extra?: Record<string, string>;
};

export type GameStatus = {
  container: string;
  online: boolean;
  players: { name: string }[];
  max_players: number;
  tps?: number;
  error?: string;
};

export type Games = {
  networks: { name: string; servers: GameServer[] }[];
  statuses: Record<string, GameStatus>;
  adapters: string[];
  servers: number;
};

function Console({ container }: { container: string }) {
  const [command, setCommand] = useState('');
  const [lines, setLines] = useState<{ text: string; error?: boolean }[]>([]);
  const [busy, setBusy] = useState(false);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!command.trim()) return;
    const sent = command;
    setCommand('');
    setBusy(true);
    setLines((prev) => [...prev, { text: `> ${sent}` }]);
    try {
      const res = await api<{ output: string; error?: string }>(
        `/api/games/${encodeURIComponent(container)}/console`,
        { command: sent },
      );
      const text = res.error || res.output || '(no output)';
      setLines((prev) => [...prev, { text, error: Boolean(res.error) }]);
    } catch (err) {
      setLines((prev) => [
        ...prev,
        { text: err instanceof Error ? err.message : String(err), error: true },
      ]);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      {lines.length > 0 && (
        <div className="logbox" style={{ maxHeight: '12rem' }}>
          {lines.map((l, i) => (
            <div key={i} className={`logline ${l.error ? 'stderr' : 'stdout'}`}>
              {l.text}
            </div>
          ))}
        </div>
      )}
      <form onSubmit={run} style={{ display: 'flex', gap: '0.5rem', padding: '0.75rem 1rem' }}>
        <input
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="Type a command, e.g. list"
          className="mono"
          style={{ fontSize: '0.85rem' }}
          disabled={busy}
        />
        <button className="btn-primary" type="submit" disabled={busy || !command.trim()}>
          Run
        </button>
      </form>
    </div>
  );
}

export function Games({ games }: { games?: Games }) {
  const [console, setConsole] = useState('');

  if (!games) return <Empty>Looking for game servers…</Empty>;
  if (games.servers === 0) {
    return (
      <Empty>
        No game servers found. The {games.adapters.join(', ') || 'game'} adapter claims containers
        by their image and environment — start one and it appears here.
      </Empty>
    );
  }

  return (
    <>
      {games.networks.map((network) => (
        <Card
          key={network.name}
          title={network.name}
          actions={
            <span className="tag">
              {network.servers.length} {network.servers.length === 1 ? 'server' : 'servers'}
            </span>
          }
          bodyless
        >
          <div className="rows">
            {network.servers.map((s) => {
              const st = games.statuses[s.container];
              const players = st?.players ?? [];
              return (
                <div key={s.container}>
                  <div className="row">
                    <div className="name">
                      <b>
                        {s.container}
                        {s.proxy && <span className="tag" style={{ marginLeft: '0.5rem' }}>proxy</span>}
                      </b>
                      <small>
                        {[s.edition, s.version, s.address].filter(Boolean).join(' · ')}
                      </small>
                    </div>

                    {st?.online ? (
                      <>
                        <span className="tag">
                          {players.length}/{st.max_players || '?'} players
                          {st.tps ? ` · ${st.tps.toFixed(1)} tps` : ''}
                        </span>
                        <div className="actions">
                          <button
                            className="icon-btn"
                            title="Console"
                            aria-label={`Console for ${s.container}`}
                            onClick={() => setConsole(console === s.container ? '' : s.container)}
                          >
                            <Terminal size={15} />
                          </button>
                        </div>
                      </>
                    ) : (
                      <Status tone="down">{st?.error ? 'no console' : 'offline'}</Status>
                    )}
                  </div>

                  {st && !st.online && st.error && (
                    <div style={{ padding: '0 1rem 0.7rem', marginTop: '-0.4rem' }}>
                      <small className="muted">{st.error}</small>
                    </div>
                  )}

                  {players.length > 0 && (
                    <div style={{ padding: '0 1rem 0.7rem', marginTop: '-0.4rem' }}>
                      <small className="muted">Online: {players.map((p) => p.name).join(', ')}</small>
                    </div>
                  )}

                  {console === s.container && <Console container={s.container} />}
                </div>
              );
            })}
          </div>
        </Card>
      ))}
    </>
  );
}
