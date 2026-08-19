import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { ActionButtons } from '../components/Actions';
import { Card, Empty, Status } from '../components/ui';
import { since } from '../lib/format';

export type Container = {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  health?: string;
  created: number;
  ports?: string[];
  project: string;
  service: string;
};

export type Stack = {
  name: string;
  containers: Container[];
  running: number;
  total: number;
  unmanaged?: boolean;
};

export type Services = {
  stacks: Stack[];
  running: number;
  total: number;
  daemon_up: boolean;
  daemon_error?: string;
  api_version?: string;
};

function containerTone(c: Container): 'up' | 'down' | 'warn' | 'bad' {
  if (c.health === 'unhealthy') return 'bad';
  if (c.health === 'starting') return 'warn';
  if (c.state === 'running') return 'up';
  if (c.state === 'restarting') return 'warn';
  return 'down';
}

function stackTone(s: Stack): 'up' | 'down' | 'warn' | 'bad' {
  if (s.running === s.total) return 'up';
  if (s.running === 0) return 'down';
  return 'warn';
}

export function Stacks({ services }: { services?: Services }) {
  // Stacks that need attention open by default; healthy ones stay collapsed.
  const [overrides, setOverrides] = useState<Record<string, boolean>>({});

  if (!services) return <Empty>Waiting for the Docker daemon…</Empty>;
  if (!services.daemon_up) {
    return (
      <Card title="Docker is unreachable">
        <p className="muted">{services.daemon_error || 'The daemon did not respond.'}</p>
      </Card>
    );
  }
  if (services.stacks.length === 0) return <Empty>No containers are defined on this host.</Empty>;

  return (
    <>
      {services.stacks.map((stack) => {
        const needsAttention = stack.running < stack.total;
        const open = overrides[stack.name] ?? needsAttention;

        return (
          <Card
            key={stack.name}
            bodyless
            title={
              <button
                className="nav-item"
                style={{ padding: 0, width: 'auto', color: 'inherit', fontWeight: 600 }}
                onClick={() => setOverrides((o) => ({ ...o, [stack.name]: !open }))}
                aria-expanded={open}
              >
                {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                {stack.name}
                {stack.unmanaged && <span className="tag">unmanaged</span>}
              </button>
            }
            actions={
              <>
                <Status tone={stackTone(stack)}>
                  {stack.running}/{stack.total}
                </Status>
                {!stack.unmanaged && (
                  <ActionButtons
                    kind="stack"
                    target={stack.name}
                    name={stack.name}
                    running={stack.running > 0}
                  />
                )}
              </>
            }
          >
            {open && (
              <div className="rows">
                {stack.containers.map((c) => (
                  <div className="row" key={c.id}>
                    <div className="name">
                      <b>{c.service || c.name}</b>
                      <small title={c.image}>{c.image}</small>
                    </div>
                    <Status tone={containerTone(c)}>
                      {c.state === 'running' ? `up ${since(c.created)}` : c.state}
                      {c.health ? ` · ${c.health}` : ''}
                    </Status>
                    <ActionButtons
                      kind="container"
                      target={c.name}
                      name={c.service || c.name}
                      running={c.state === 'running'}
                    />
                  </div>
                ))}
              </div>
            )}
          </Card>
        );
      })}
    </>
  );
}
