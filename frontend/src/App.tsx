import { useEffect } from 'react';
import { Navigate, NavLink, Route, Routes, useNavigate } from 'react-router-dom';
import { clearTokens, getRefreshToken, isAuthenticated } from './lib/auth';
import { api } from './lib/api';
import { LoginPage } from './pages/Login';
import { ChatPage } from './pages/Chat';
import { GraphPage } from './pages/Graph';

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  if (!isAuthenticated()) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

function Nav() {
  if (!isAuthenticated()) return null;
  return (
    <nav>
      <NavLink to="/">Chat</NavLink>
      <NavLink to="/graph">Graph</NavLink>
      <span className="spacer" />
      <NavLink to="/logout">Logout</NavLink>
    </nav>
  );
}

export function App() {
  return (
    <>
      <Nav />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        {/* Email links point at /auth/magic-link?token=...; reuse Login. */}
        <Route path="/auth/magic-link" element={<LoginPage />} />
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <ChatPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/graph"
          element={
            <ProtectedRoute>
              <GraphPage />
            </ProtectedRoute>
          }
        />
        <Route path="/logout" element={<LogoutRedirect />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </>
  );
}

function LogoutRedirect() {
  const navigate = useNavigate();
  useEffect(() => {
    const refresh = getRefreshToken();
    const done = () => {
      clearTokens();
      navigate('/login', { replace: true });
    };
    if (!refresh) {
      done();
      return;
    }
    // Logout is idempotent on the server; we always clear local tokens.
    api('/v1/auth/logout', { method: 'POST', body: { refresh_token: refresh } })
      .catch(() => undefined)
      .finally(done);
  }, [navigate]);
  return <main>Выходим…</main>;
}
