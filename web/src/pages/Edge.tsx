import { Card, Empty, Status } from '../components/ui';

export type Edge = {
  sites: {
    host: string;
    issuer?: string;
    not_after?: string;
    days_left: number;
    reachable: boolean;
    error?: string;
  }[];
  public_ip?: string;
  tailscale: {
    running: boolean;
    self?: string;
    peers: { name: string; online: boolean; os?: string; last_ip?: string }[];
  };
  soonest_expiry_days: number;
  error?: string;
};

function certTone(days: number): 'up' | 'warn' | 'bad' {
  if (days <= 7) return 'bad';
  if (days <= 21) return 'warn';
  return 'up';
}

export function Edge({ edge }: { edge?: Edge }) {
  if (!edge) return <Empty>Checking certificates and peers…</Empty>;

  return (
    <>
      <div className="grid">
        <Card title="Public address">
          <div className="stat">
            <span className="label">Public IP</span>
            <span className="value mono" style={{ fontSize: '1.25rem' }}>
              {edge.public_ip || 'unknown'}
            </span>
          </div>
        </Card>
        <Card title="Certificates">
          <div className="stat">
            <span className="label">Soonest expiry</span>
            <span className="value">
              {edge.soonest_expiry_days >= 0 ? `${edge.soonest_expiry_days} days` : '—'}
            </span>
            <span className="sub">{edge.sites.filter((s) => s.reachable).length} sites served</span>
          </div>
        </Card>
        <Card title="Tailscale">
          <div className="stat">
            <span className="label">Tailnet</span>
            <span className="value" style={{ fontSize: '1.1rem' }}>
              {edge.tailscale.running ? 'connected' : 'down'}
            </span>
            <span className="sub">
              {edge.tailscale.peers.filter((p) => p.online).length} of{' '}
              {edge.tailscale.peers.length} peers online
            </span>
          </div>
        </Card>
      </div>

      <Card
        title={`${edge.sites.length} routed hostnames`}
        actions={<span className="tag">from Caddy</span>}
        bodyless
      >
        {edge.error && <div className="empty">{edge.error}</div>}
        <div className="rows">
          {edge.sites.map((s) => (
            <div className="row" key={s.host}>
              <div className="name">
                <b>{s.host}</b>
                <small>{s.reachable ? s.issuer || 'certificate' : s.error}</small>
              </div>
              {s.reachable ? (
                <>
                  <span className="tag">{new Date(s.not_after!).toLocaleDateString()}</span>
                  <Status tone={certTone(s.days_left)}>{s.days_left} days left</Status>
                </>
              ) : (
                <Status tone="bad">unreachable</Status>
              )}
            </div>
          ))}
        </div>
      </Card>

      <Card title="Tailscale peers" bodyless>
        <div className="rows">
          {edge.tailscale.self && (
            <div className="row">
              <div className="name">
                <b>{edge.tailscale.self}</b>
                <small>this host</small>
              </div>
              <Status tone={edge.tailscale.running ? 'up' : 'down'}>
                {edge.tailscale.running ? 'running' : 'stopped'}
              </Status>
            </div>
          )}
          {edge.tailscale.peers.map((p) => (
            <div className="row" key={p.name}>
              <div className="name">
                <b>{p.name}</b>
                <small>
                  {p.os}
                  {p.last_ip ? ` · ${p.last_ip}` : ''}
                </small>
              </div>
              <Status tone={p.online ? 'up' : 'down'}>{p.online ? 'online' : 'offline'}</Status>
            </div>
          ))}
        </div>
      </Card>
    </>
  );
}
