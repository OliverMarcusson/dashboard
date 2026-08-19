import { AlertTriangle, CheckCircle2 } from 'lucide-react';
import { Card, Empty, Stat, Status } from '../components/ui';
import { Sparkline } from '../components/Sparkline';
import { useHistory } from '../lib/history';
import { bytes, duration, percent, rate } from '../lib/format';
import type { Services } from './Stacks';

export type HostMetrics = {
  cpu_percent: number;
  mem_used: number;
  mem_total: number;
  mem_percent: number;
  load1: number;
  load5: number;
  load15: number;
  uptime_secs: number;
  hostname: string;
  platform: string;
  kernel_version: string;
  net_rx_per_sec: number;
  net_tx_per_sec: number;
  filesystems: { mount: string; used: number; total: number; percent: number; fstype: string }[];
  temps?: { sensor: string; value: number }[];
};

export type Units = { units: { name: string; failed: boolean }[]; failed: number; running: number };

type Concern = { text: string; detail: string; tone: 'warn' | 'bad' };

/** Everything that deserves attention, derived from what is already streaming. */
function concerns(host?: HostMetrics, services?: Services, units?: Units): Concern[] {
  const out: Concern[] = [];

  for (const fs of host?.filesystems ?? []) {
    if (fs.percent >= 85) {
      out.push({
        text: `${fs.mount} is ${percent(fs.percent)} full`,
        detail: `${bytes(fs.used)} of ${bytes(fs.total)}`,
        tone: fs.percent >= 95 ? 'bad' : 'warn',
      });
    }
  }
  for (const stack of services?.stacks ?? []) {
    if (stack.running < stack.total) {
      out.push({
        text: `${stack.name} is not fully running`,
        detail: `${stack.running} of ${stack.total} containers up`,
        tone: stack.running === 0 ? 'bad' : 'warn',
      });
    }
    for (const c of stack.containers) {
      if (c.health === 'unhealthy') {
        out.push({ text: `${c.name} is unhealthy`, detail: c.status, tone: 'bad' });
      }
    }
  }
  for (const unit of units?.units ?? []) {
    if (unit.failed) {
      out.push({ text: `${unit.name} failed`, detail: 'systemd service', tone: 'bad' });
    }
  }
  if (host && host.mem_percent >= 90) {
    out.push({
      text: `Memory at ${percent(host.mem_percent)}`,
      detail: `${bytes(host.mem_used)} of ${bytes(host.mem_total)}`,
      tone: 'warn',
    });
  }
  return out;
}

export function Home({
  host,
  services,
  units,
}: {
  host?: HostMetrics;
  services?: Services;
  units?: Units;
}) {
  const cpu = useHistory('cpu');
  const mem = useHistory('mem');
  const net = useHistory('net_rx');
  const issues = concerns(host, services, units);

  if (!host) return <Empty>Waiting for the first host sample…</Empty>;

  return (
    <>
      <Card
        title={issues.length ? `${issues.length} things need attention` : 'All systems nominal'}
        actions={
          issues.length ? (
            <AlertTriangle size={16} color="var(--warn)" />
          ) : (
            <CheckCircle2 size={16} color="var(--ok)" />
          )
        }
        bodyless={issues.length > 0}
      >
        {issues.length === 0 ? (
          <p className="muted" style={{ margin: 0 }}>
            {services?.running ?? 0} containers running across {services?.stacks.length ?? 0}{' '}
            stacks · {units?.running ?? 0} services · nothing degraded.
          </p>
        ) : (
          <div className="rows">
            {issues.map((c, i) => (
              <div className="row" key={i}>
                <div className="name">
                  <b>{c.text}</b>
                  <small>{c.detail}</small>
                </div>
                <Status tone={c.tone}>{c.tone === 'bad' ? 'critical' : 'warning'}</Status>
              </div>
            ))}
          </div>
        )}
      </Card>

      <div className="grid">
        <Card title="Processor">
          <Stat label="CPU" value={percent(host.cpu_percent, 1)} percent={host.cpu_percent} />
          <Sparkline points={cpu} max={100} />
          <p className="sub muted" style={{ margin: 0 }}>
            load {host.load1.toFixed(2)} · {host.load5.toFixed(2)} · {host.load15.toFixed(2)}
          </p>
        </Card>

        <Card title="Memory">
          <Stat
            label="Used"
            value={bytes(host.mem_used)}
            sub={`of ${bytes(host.mem_total)}`}
            percent={host.mem_percent}
          />
          <Sparkline points={mem} max={100} tone="ok" />
        </Card>

        <Card title="Network">
          <Stat label="Receive" value={rate(host.net_rx_per_sec)} sub={`send ${rate(host.net_tx_per_sec)}`} />
          <Sparkline points={net} tone="warn" />
        </Card>

        <Card title="Host">
          <Stat label="Uptime" value={duration(host.uptime_secs)} sub={host.hostname} />
          <p className="sub muted" style={{ margin: '0.5rem 0 0' }}>
            {host.platform} · {host.kernel_version}
          </p>
          {host.temps?.length ? (
            <p className="sub muted" style={{ margin: '0.25rem 0 0' }}>
              {host.temps
                .slice(0, 3)
                .map((t) => `${t.sensor} ${t.value.toFixed(0)}°C`)
                .join(' · ')}
            </p>
          ) : null}
        </Card>
      </div>

      <Card title="Filesystems" bodyless>
        <div className="rows">
          {host.filesystems.map((fs) => (
            <div className="row" key={fs.mount}>
              <div className="name">
                <b>{fs.mount}</b>
                <small>{fs.fstype}</small>
              </div>
              <div style={{ minWidth: '11rem' }}>
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
    </>
  );
}
