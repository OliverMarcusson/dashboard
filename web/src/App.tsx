import { useEffect, useState } from 'react';
import {
  Activity,
  Boxes,
  Gamepad2,
  HardDrive,
  KeyRound,
  LogOut,
  RefreshCw,
  ScrollText,
  Server,
  Globe,
} from 'lucide-react';
import { api, type Session } from './api';
import { useTopics } from './lib/ws';
import { Stacks, type Services } from './pages/Stacks';
import { Home, type HostMetrics, type Units } from './pages/Home';
import { Logs } from './pages/Logs';
import { Jobs, type Job } from './pages/Jobs';
import { Storage, type Storage as StorageData } from './pages/Storage';
import { Edge, type Edge as EdgeData } from './pages/Edge';
import { Updates, type Updates as UpdatesData } from './pages/Updates';
import { Games, type Games as GamesData } from './pages/Games';
import { Security } from './pages/Security';

type Topics = {
  'host.metrics': HostMetrics;
  'docker.services': Services;
  'systemd.units': Units;
  jobs: Job;
  'probe.storage': StorageData;
  'probe.edge': EdgeData;
  'probe.updates': UpdatesData;
  games: GamesData;
};

const NAV = [
  { path: '/', label: 'Home', Icon: Activity },
  { path: '/stacks', label: 'Stacks', Icon: Boxes },
  { path: '/storage', label: 'Storage', Icon: HardDrive },
  { path: '/edge', label: 'Edge', Icon: Globe },
  { path: '/updates', label: 'Updates', Icon: RefreshCw },
  { path: '/games', label: 'Games', Icon: Gamepad2 },
  { path: '/logs', label: 'Logs', Icon: ScrollText },
  { path: '/jobs', label: 'Jobs', Icon: Server },
  { path: '/security', label: 'Security', Icon: KeyRound },
] as const;

export function App({ session, onSignedOut }: { session: Session & { authenticated: true }; onSignedOut: () => void }) {
  const [path, setPath] = useState(location.pathname);
  const { data, state } = useTopics<Topics>([
    'host.metrics',
    'docker.services',
    'systemd.units',
    'jobs',
    'probe.storage',
    'probe.edge',
    'probe.updates',
    'games',
  ]);

  useEffect(() => {
    const onPop = () => setPath(location.pathname);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  }, []);

  const go = (to: string) => {
    history.pushState(null, '', to);
    setPath(to);
  };

  const services = data['docker.services'];
  const host = data['host.metrics'];
  const units = data['systemd.units'];
  const degraded = services?.stacks.filter((s) => s.running < s.total).length ?? 0;
  const current = NAV.find((n) => n.path === path) ?? NAV[0];

  return (
    <div className="shell">
      <nav className="sidebar">
        <div className="brand">
          <Server size={17} />
          <div>
            Dashboard
            <div className="host">{session.username}</div>
          </div>
        </div>

        {NAV.map(({ path: to, label, Icon }) => (
          <button
            key={to}
            className="nav-item"
            aria-current={to === current.path ? 'page' : undefined}
            onClick={() => go(to)}
          >
            <Icon size={16} />
            {label}
            {to === '/stacks' && degraded > 0 && <span className="badge">{degraded}</span>}
          </button>
        ))}

        <span className="nav-spacer" />
        <button
          className="nav-item"
          onClick={async () => {
            await api('/api/auth/logout', {});
            onSignedOut();
          }}
        >
          <LogOut size={16} />
          Sign out
        </button>
      </nav>

      <main className="main">
        <header className="topbar">
          <h1>{current.label}</h1>
          <span className="spacer" />
          <span className={`conn ${state}`}>
            <span className="dot" />
            {state === 'open' ? 'live' : state}
          </span>
        </header>

        <div className="content">
          {current.path === '/' && <Home host={host} services={services} units={units} />}
          {current.path === '/stacks' && <Stacks services={services} />}
          {current.path === '/logs' && <Logs services={services} units={units} />}
          {current.path === '/jobs' && <Jobs live={data.jobs} />}
          {current.path === '/storage' && (
            <Storage storage={data['probe.storage']} host={host} />
          )}
          {current.path === '/edge' && <Edge edge={data['probe.edge']} />}
          {current.path === '/updates' && <Updates updates={data['probe.updates']} />}
          {current.path === '/games' && <Games games={data.games} />}
          {current.path === '/security' && <Security />}
        </div>
      </main>
    </div>
  );
}
