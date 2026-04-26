import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '../lib/api';

const NODE_TYPES = [
  'thought',
  'idea',
  'memory',
  'dream',
  'emotion',
  'task',
  'event',
  'person',
  'place',
  'topic',
] as const;

type NodeType = (typeof NODE_TYPES)[number];

type Node = {
  id: string;
  type: NodeType;
  title?: string | null;
  content?: string | null;
  metadata?: unknown;
  occurred_at?: string | null;
  occurred_at_precision?: string | null;
  activation: number;
  last_accessed_at: string;
  pinned: boolean;
  source_message_id?: string | null;
  created_at: string;
  updated_at: string;
};

type Edge = {
  id: string;
  source_id: string;
  target_id: string;
  type: string;
  weight: number;
  created_at: string;
};

type GraphResp = { nodes: Node[]; edges: Edge[] };

export function GraphPage() {
  const [selectedTypes, setSelectedTypes] = useState<Set<NodeType>>(new Set());
  const [since, setSince] = useState('');
  const [limit, setLimit] = useState(200);
  const [showRaw, setShowRaw] = useState(false);

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

  const nodeById = useMemo(() => {
    const m = new Map<string, Node>();
    graph.data?.nodes.forEach((n) => m.set(n.id, n));
    return m;
  }, [graph.data]);

  function toggleType(t: NodeType) {
    setSelectedTypes((prev) => {
      const next = new Set(prev);
      if (next.has(t)) next.delete(t);
      else next.add(t);
      return next;
    });
  }

  return (
    <main>
      <h1>Graph</h1>
      <p className="muted">
        Сырая выдача <code>GET /v1/graph</code>. Граф-визуализация — следующий шаг (спайк H16).
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
          <label>
            <input
              type="checkbox"
              checked={showRaw}
              onChange={(e) => setShowRaw(e.target.checked)}
              style={{ width: 'auto', marginRight: 4 }}
            />
            Показать сырой JSON
          </label>
        </div>
      </section>

      {graph.error && <p className="error">{humanize(graph.error)}</p>}

      {graph.data && (
        <>
          <p className="muted">
            <strong>{graph.data.nodes.length}</strong> узлов · <strong>{graph.data.edges.length}</strong> связей
          </p>

          {showRaw && (
            <details open>
              <summary>JSON</summary>
              <pre>{JSON.stringify(graph.data, null, 2)}</pre>
            </details>
          )}

          <section style={{ marginTop: '1rem' }}>
            <h2>Узлы</h2>
            <ul className="list">
              {graph.data.nodes.map((n) => (
                <li key={n.id} className="card">
                  <div className="row">
                    <span style={{ background: '#f0f0f0', padding: '0.1rem 0.4rem', borderRadius: 3, fontSize: '0.75rem' }}>
                      {n.type}
                    </span>
                    <strong>{n.title ?? '(без названия)'}</strong>
                    <span className="spacer" style={{ flex: 1 }} />
                    <span className="muted" style={{ fontSize: '0.75rem' }}>
                      activation {n.activation.toFixed(2)} · {new Date(n.created_at).toLocaleString()}
                    </span>
                  </div>
                  {n.content && <div style={{ marginTop: '0.25rem' }}>{n.content}</div>}
                </li>
              ))}
              {graph.data.nodes.length === 0 && <li className="muted">Граф пуст. Иди в чат и напиши что-нибудь.</li>}
            </ul>
          </section>

          <section style={{ marginTop: '1rem' }}>
            <h2>Связи</h2>
            <ul className="list">
              {graph.data.edges.map((e) => {
                const src = nodeById.get(e.source_id);
                const tgt = nodeById.get(e.target_id);
                return (
                  <li key={e.id} className="card">
                    <span>{src?.title ?? e.source_id.slice(0, 8)}</span>
                    {' — '}
                    <em>{e.type}</em>
                    {' → '}
                    <span>{tgt?.title ?? e.target_id.slice(0, 8)}</span>
                    <span className="muted" style={{ marginLeft: '0.5rem', fontSize: '0.75rem' }}>
                      weight {e.weight.toFixed(2)}
                    </span>
                  </li>
                );
              })}
              {graph.data.edges.length === 0 && <li className="muted">Связей пока нет.</li>}
            </ul>
          </section>
        </>
      )}
    </main>
  );
}

function humanize(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return 'Неизвестная ошибка';
}
