import { Card, Empty, Status } from '../components/ui';
import { bytes, percent } from '../lib/format';
import type { HostMetrics } from './Home';

export type Storage = {
  docker: { kind: string; total: number; active: number; size: number; reclaimable: number }[];
  reclaimable_total: number;
  drives: {
    device: string;
    model?: string;
    healthy: boolean;
    temperature?: number;
    power_on_hours?: number;
    percent_used?: number;
    capacity?: number;
    error?: string;
  }[];
  error?: string;
};

export function Storage({ storage, host }: { storage?: Storage; host?: HostMetrics }) {
  if (!storage) return <Empty>Measuring disk usage…</Empty>;

  return (
    <>
      <Card title="Filesystems" bodyless>
        <div className="rows">
          {(host?.filesystems ?? []).map((fs) => (
            <div className="row" key={fs.mount}>
              <div className="name">
                <b>{fs.mount}</b>
                <small>{fs.fstype}</small>
              </div>
              <div style={{ minWidth: '12rem' }}>
                <div className="sub" style={{ marginBottom: 4 }}>
                  {bytes(fs.used)} of {bytes(fs.total)}
                </div>
                <div className={`bar ${fs.percent >= 90 ? 'crit' : fs.percent >= 75 ? 'warn' : ''}`}>
                  <span style={{ width: `${fs.percent}%` }} />
                </div>
              </div>
              <span className="tag">{percent(fs.percent)}</span>
            </div>
          ))}
        </div>
      </Card>

      <Card
        title="Docker"
        actions={
          storage.reclaimable_total > 0 ? (
            <Status tone={storage.reclaimable_total > 20 * 2 ** 30 ? 'warn' : 'up'}>
              {bytes(storage.reclaimable_total)} reclaimable
            </Status>
          ) : undefined
        }
        bodyless
      >
        <div className="rows">
          {storage.docker.map((d) => (
            <div className="row" key={d.kind}>
              <div className="name">
                <b>{d.kind}</b>
                <small>
                  {d.active} of {d.total} in use
                </small>
              </div>
              <span className="tag">{bytes(d.size)}</span>
              <span className="tag" style={d.reclaimable > 0 ? { color: 'var(--warn)' } : undefined}>
                {d.reclaimable > 0 ? `${bytes(d.reclaimable)} free-able` : 'nothing to reclaim'}
              </span>
            </div>
          ))}
        </div>
      </Card>

      <Card title="Drive health" bodyless>
        {storage.drives.length === 0 ? (
          <Empty>No SMART-capable drives were reported.</Empty>
        ) : (
          <div className="rows">
            {storage.drives.map((d) => (
              <div className="row" key={d.device}>
                <div className="name">
                  <b>{d.model || d.device}</b>
                  <small>
                    {d.device}
                    {d.capacity ? ` · ${bytes(d.capacity)}` : ''}
                    {d.power_on_hours ? ` · ${d.power_on_hours.toLocaleString()} h powered on` : ''}
                  </small>
                </div>
                {d.percent_used !== undefined && d.percent_used > 0 && (
                  <span className="tag">{d.percent_used}% life used</span>
                )}
                <Status tone={d.error ? 'warn' : d.healthy ? 'up' : 'bad'}>
                  {d.error ? 'unknown' : d.healthy ? 'healthy' : 'failing'}
                  {d.temperature ? ` · ${d.temperature}°C` : ''}
                </Status>
              </div>
            ))}
          </div>
        )}
      </Card>
    </>
  );
}
