import { useState } from 'react';
import { startRegistration } from '@simplewebauthn/browser';
import { api } from './api';

type StartResponse = { state_id: string; options: { publicKey: unknown } };

export function Enroll({ onEnrolled }: { onEnrolled: () => void }) {
  const [code, setCode] = useState('');
  const [name, setName] = useState('Proton Pass');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function register(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const start = await api<StartResponse>('/api/auth/enroll/start', {
        code,
        device_name: name.trim() || 'Passkey',
      });
      const credential = await startRegistration({
        optionsJSON: start.options.publicKey as never,
      });
      await api('/api/auth/enroll/finish', { state_id: start.state_id, credential });
      onEnrolled();
    } catch (e) {
      if (e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')) {
        setError('Registration was cancelled. The code is spent — generate a new one to retry.');
      } else {
        setError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="gate">
      <form className="gate-card" onSubmit={register}>
        <div>
          <h1>Register a passkey</h1>
          <p className="sub">
            Run <span className="mono">dashboardd enroll</span> on the server for a one-time code.
            It authorizes registration only — your password manager creates and keeps the passkey.
          </p>
        </div>

        <div className="field">
          <label htmlFor="code">Enrollment code</label>
          <input
            id="code"
            className="code"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="XXXX-XXXX-XXXX"
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            required
          />
        </div>

        <div className="field">
          <label htmlFor="name">Name this passkey</label>
          <input id="name" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>

        <button className="btn-primary" type="submit" disabled={busy || !code}>
          {busy ? 'Waiting for your authenticator…' : 'Register passkey'}
        </button>

        {error && <div className="notice">{error}</div>}

        <a className="linkish center" href="/">
          Back to sign in
        </a>
      </form>
    </div>
  );
}
