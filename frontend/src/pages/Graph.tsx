import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '../lib/api';
import { ForceGraphView } from './graph/ForceGraphView';
import { NODE_TYPES, TYPE_COLOR } from './graph/types';
import type { GraphResp, NodeType } from './graph/types';

export function GraphPage() {
  const [selectedTypes, setSelectedTypes] = useState<Set<NodeType>>(new Set());
  const [since, setSince] = useState('');
  const [limit, setLimit] = useState(200);

  const queryString = useMemo(() => {
    const params = new URLSearchParams();
    if (selectedTypes.size > 0) params.set('type', Array.from(selectedTypes).join(','));
    if (since) params.set('since', since);
    params.set('limit', String(limit));
    return params.toString();
  }, [selectedTypes, since, limit]);

  const graph = useQuery({
    queryKey: ['graph', queryString],
    queryFn: () => api<GraphResp>(`/v1/graph?${queryString}`),
  });

  const containerRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 800, h: 560 });
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      const r = el.getBoundingClientRect();
      setSize({ w: Math.max(320, Math.floor(r.width)), h: Math.max(320, Math.floor(r.height)) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  function toggleType(t: NodeType) {
    setSelectedTypes((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });
  }

  return (
    <main style={{ maxWidth: 1200 }}>
      <h1>Graph</h1>
      <p className="muted">
        Граф жизни. Узлы с фотографиями отображаются круглыми портретами, остальные —
        цветными кружками по типу. Анимация физическая: drag тянет соседей через пружины.
      </p>

      <section className="card" style={{ marginBottom: '1rem' }}>
        <div className="col">
          <div>
            <strong>Типы:</strong>{' '}
            {NODE_TYPES.map((t) => (
              <label key={t} style={{ marginRight: '0.75rem', whiteSpace: 'nowrap' }}>
                <input
                  type="checkbox"
                  checked={selectedTypes.has(t)}
                  onChange={() => toggleType(t)}
                  style={{ width: 'auto', marginRight: 4 }}
                />
                <span
                  style={{
                    display: 'inline-block',
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: TYPE_COLOR[t],
                    marginRight: 4,
                    verticalAlign: 'middle',
                  }}
                />
                {t}
              </label>
            ))}
          </div>
          <div className="row">
            <label style={{ flex: 1 }}>
              Since (RFC3339)
              <input
                type="text"
                value={since}
                onChange={(e) => setSince(e.target.value)}
                placeholder="2026-01-01T00:00:00Z"
              />
            </label>
            <label style={{ width: 120 }}>
              Limit
              <input
                type="number"
                min={1}
                max={1000}
                value={limit}
                onChange={(e) => setLimit(Number(e.target.value) || 200)}
              />
            </label>
            <button onClick={() => graph.refetch()} disabled={graph.isFetching}>
              {graph.isFetching ? 'Загрузка…' : 'Обновить'}
            </button>
          </div>
        </div>
      </section>

      {graph.data && (
        <div className="row" style={{ marginBottom: '0.5rem' }}>
          <span className="spacer" style={{ flex: 1 }} />
          <span className="muted" style={{ fontSize: '0.8rem' }}>
            {graph.data.nodes.length} узлов · {graph.data.edges.length} связей
          </span>
        </div>
      )}

      <div
        ref={containerRef}
        style={{
          height: '70vh',
          minHeight: 480,
          border: '1px solid var(--border-subtle)',
          borderRadius: 'var(--radius-lg)',
          background: 'var(--bg-input)',
          position: 'relative',
          overflow: 'hidden',
        }}
      >
        {graph.error && (
          <p className="error" style={{ padding: '1rem' }}>
            {humanize(graph.error)}
          </p>
        )}
        {graph.data && graph.data.nodes.length === 0 && (
          <p className="muted" style={{ padding: '1rem' }}>
            Граф пуст. Иди в чат и напиши что-нибудь.
          </p>
        )}
        {graph.data && graph.data.nodes.length > 0 && (
          <ForceGraphView data={graph.data} width={size.w} height={size.h} />
        )}
      </div>
    </main>
  );
}

function humanize(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return 'Неизвестная ошибка';
}
