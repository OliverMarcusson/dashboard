import { useState } from 'react';
import { startAuthentication } from '@simplewebauthn/browser';
import { api } from './api';

type StartResponse = { state_id: string; options: { publicKey: unknown } };

export function Login({ onSignedIn, hasPasskeys }: { onSignedIn: () => void; hasPasskeys: boolean }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function signIn() {
    setError('');
    setBusy(true);
    try {
      const start = await api<StartResponse>('/api/auth/login/start', {});
      const credential = await startAuthentication({
        optionsJSON: start.options.publicKey as never,
      });
      await api('/api/auth/login/finish', { state_id: start.state_id, credential });
      onSignedIn();
    } catch (e) {
      // A cancelled prompt is a choice, not a failure worth shouting about.
      if (e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')) {
        setError('');
      } else {
        setError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="gate">
      <div className="gate-card">
        <div>
          <h1>Dashboard</h1>
          <p className="sub">Sign in with your passkey.</p>
        </div>

        {hasPasskeys ? (
          <button className="btn-primary" onClick={signIn} disabled={busy}>
            {busy ? 'Waiting for your passkey…' : 'Continue with passkey'}
          </button>
        ) : (
          <div className="notice info">
            No passkey is enrolled yet. Run <span className="mono">dashboardd enroll</span> on the
            server to get a code, then register one.
          </div>
        )}

        {error && <div className="notice">{error}</div>}

        <a className="linkish center" href="/enroll">
          Register a new passkey
        </a>
      </div>
    </div>
  );
}
