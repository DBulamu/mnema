import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { isAuthenticated, setTokens } from '../lib/auth';

type ConsumeResponse = {
  access_token: string;
  refresh_token: string;
  user: { id: string; email: string };
};

export function LoginPage() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();

  const [email, setEmail] = useState('');
  const [token, setToken] = useState('');
  const [phase, setPhase] = useState<'idle' | 'requested' | 'consuming'>('idle');
  const [error, setError] = useState<string | null>(null);
  // Magic-link tokens are single-use. React 18 StrictMode runs effects
  // twice in dev — without this guard, both runs POST /consume and the
  // second one races back with 401 ("token already consumed"), clobbering
  // the success state from the first run.
  const consumedRef = useRef(false);

  // If we land here already authenticated, bounce home — covers stale /login
  // bookmarks and the post-logout redirect race.
  useEffect(() => {
    if (isAuthenticated()) navigate('/', { replace: true });
  }, [navigate]);

  // Magic-link emails embed `?token=...`. When we land back on /login with
  // that param, run consume automatically.
  useEffect(() => {
    const t = params.get('token');
    if (!t) return;
    if (consumedRef.current) return;
    consumedRef.current = true;
    setToken(t);
    void doConsume(t).finally(() => {
      // Strip the token from the URL so a refresh doesn't re-consume.
      params.delete('token');
      setParams(params, { replace: true });
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function doRequest(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await api('/v1/auth/magic-link/request', {
        method: 'POST',
        body: { email },
        skipAuth: true,
      });
      setPhase('requested');
    } catch (err) {
      setError(humanize(err));
    }
  }

  async function doConsume(t: string) {
    setError(null);
    setPhase('consuming');
    try {
      const res = await api<ConsumeResponse>('/v1/auth/magic-link/consume', {
        method: 'POST',
        body: { token: t },
        skipAuth: true,
      });
      setTokens(res.access_token, res.refresh_token);
      navigate('/', { replace: true });
    } catch (err) {
      setError(humanize(err));
      setPhase('requested');
    }
  }

  return (
    <main>
      <h1>Mnema</h1>
      <p className="muted">Вход по magic-link. В local-stack письма уходят в mailpit на http://localhost:8025.</p>

      <form onSubmit={doRequest} className="col" style={{ maxWidth: 360 }}>
        <label>
          Email
          <input
            type="email"
            value={email}
            required
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
            disabled={phase === 'consuming'}
          />
        </label>
        <button type="submit" className="primary" disabled={phase === 'consuming' || !email}>
          Отправить ссылку
        </button>
      </form>

      {phase === 'requested' && (
        <section style={{ marginTop: '1.5rem' }}>
          <p>
            Письмо отправлено. Открой <a href="http://localhost:8025" target="_blank" rel="noreferrer">mailpit</a> и
            перейди по ссылке, либо вставь токен из письма сюда:
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void doConsume(token);
            }}
            className="row"
          >
            <input
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="токен из письма"
            />
            <button type="submit" className="primary" disabled={!token}>
              Войти
            </button>
          </form>
        </section>
      )}

      {phase === 'consuming' && <p className="muted">Проверяем токен…</p>}
      {error && <p className="error">{error}</p>}
    </main>
  );
}

function humanize(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return 'Неизвестная ошибка';
}
