import { StrictMode, useCallback, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { api, type Session } from './api';
import { Login } from './Login';
import { Enroll } from './Enroll';
import { App as Shell } from './App';
import './styles.css';

function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [path, setPath] = useState(location.pathname);

  const refresh = useCallback(async () => {
    setSession(await api<Session>('/api/auth/session'));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useEffect(() => {
    const onPop = () => setPath(location.pathname);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  }, []);

  const go = useCallback((to: string) => {
    history.pushState(null, '', to);
    setPath(to);
  }, []);

  if (!session) {
    return <div className="gate"><p className="muted">Loading…</p></div>;
  }

  if (path === '/enroll') {
    return (
      <Enroll
        onEnrolled={() => {
          go('/');
          refresh();
        }}
      />
    );
  }

  if (!session.authenticated) {
    return <Login onSignedIn={refresh} hasPasskeys={session.has_passkeys} />;
  }

  return <Shell session={session} onSignedOut={refresh} />;
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
