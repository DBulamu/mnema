import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '../../lib/api';
import { TYPE_COLOR, type GraphNode, type GraphEdge, type NodeType } from './types';

// Backend response shape for GET /v1/nodes/{id}.
type NodeDetail = {
  node: GraphNode & {
    occurred_at?: string | null;
    occurred_at_precision?: 'day' | 'month' | 'year' | null;
    pinned: boolean;
    last_accessed_at: string;
    source_message_id?: string | null;
  };
  edges: GraphEdge[];
  neighbors: GraphNode[];
};

type Props = {
  nodeId: string;
  // onClose hides the panel; onSelect navigates to a neighbour without
  // closing the panel — the parent swaps the active id.
  onClose: () => void;
  onSelect: (id: string) => void;
};

// NodeDetailPanel renders the node's fields plus its 1-hop neighbours.
// Neighbours are clickable: clicking one calls onSelect and the panel
// re-fetches for the new id. This is the basic "open node" UX —
// timeline navigation and richer affordances live in later phases.
export function NodeDetailPanel({ nodeId, onClose, onSelect }: Props) {
  const detail = useQuery({
    queryKey: ['node', nodeId],
    queryFn: () => api<NodeDetail>(`/v1/nodes/${nodeId}`),
  });

  return (
    <aside className="card" style={panelStyle}>
      <header style={headerStyle}>
        <strong>Узел</strong>
        <button onClick={onClose} aria-label="закрыть" style={closeBtnStyle}>
          ×
        </button>
      </header>

      {detail.isLoading && <p className="muted">Загрузка…</p>}
      {detail.error && (
        <p className="error" style={{ fontSize: '0.85rem' }}>
          {humanize(detail.error)}
        </p>
      )}

      {detail.data && (
        <>
          <NodeHeader node={detail.data.node} />
          <NodeBody node={detail.data.node} />
          <NeighborList
            edges={detail.data.edges}
            neighbors={detail.data.neighbors}
            currentId={detail.data.node.id}
            onSelect={onSelect}
          />
        </>
      )}
    </aside>
  );
}

function NodeHeader({ node }: { node: NodeDetail['node'] }) {
  const color = TYPE_COLOR[node.type as NodeType] ?? '#888';
  return (
    <div style={{ marginBottom: '0.75rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
        <span
          style={{
            display: 'inline-block',
            width: 10,
            height: 10,
            borderRadius: '50%',
            background: color,
          }}
        />
        <span className="muted" style={{ fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
          {node.type}
        </span>
        {node.pinned && (
          <span className="muted" style={{ fontSize: '0.7rem' }}>
            • pinned
          </span>
        )}
      </div>
      {node.title && (
        <h3 style={{ fontSize: '1.05rem', margin: 0, lineHeight: 1.3 }}>{node.title}</h3>
      )}
    </div>
  );
}

function NodeBody({ node }: { node: NodeDetail['node'] }) {
  return (
    <div style={{ fontSize: '0.85rem', marginBottom: '0.75rem' }}>
      {node.content && (
        <p style={{ marginTop: 0, marginBottom: '0.6rem', lineHeight: 1.4, whiteSpace: 'pre-wrap' }}>
          {node.content}
        </p>
      )}
      <dl style={dlStyle}>
        {node.occurred_at && (
          <Row label="Когда">
            {formatOccurred(node.occurred_at, node.occurred_at_precision)}
          </Row>
        )}
        <Row label="Записано">{formatDate(node.created_at)}</Row>
        <Row label="Активация">{node.activation.toFixed(2)}</Row>
        {node.image_url && (
          <Row label="Фото">
            <a href={node.image_url} target="_blank" rel="noreferrer">
              открыть
            </a>
          </Row>
        )}
      </dl>
    </div>
  );
}

function NeighborList({
  edges,
  neighbors,
  currentId,
  onSelect,
}: {
  edges: GraphEdge[];
  neighbors: GraphNode[];
  currentId: string;
  onSelect: (id: string) => void;
}) {
  if (neighbors.length === 0) {
    return <p className="muted" style={{ fontSize: '0.8rem' }}>Соседей нет.</p>;
  }
  // Map neighbour id → edge type from the *current* node's perspective
  // so the user sees "→ part_of" or "← mentions" rather than a plain
  // adjacency list.
  const byNeighbor = new Map<string, { type: string; outgoing: boolean }>();
  for (const e of edges) {
    if (e.source_id === currentId && e.target_id !== currentId) {
      byNeighbor.set(e.target_id, { type: e.type, outgoing: true });
    } else if (e.target_id === currentId && e.source_id !== currentId) {
      byNeighbor.set(e.source_id, { type: e.type, outgoing: false });
    }
  }
  return (
    <div>
      <div className="muted" style={{ fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 6 }}>
        Соседи · {neighbors.length}
      </div>
      <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
        {neighbors.map((nb) => {
          const rel = byNeighbor.get(nb.id);
          const color = TYPE_COLOR[nb.type as NodeType] ?? '#888';
          return (
            <li key={nb.id} style={{ marginBottom: 4 }}>
              <button
                onClick={() => onSelect(nb.id)}
                style={neighborBtnStyle}
                title={nb.title ?? ''}
              >
                <span
                  style={{
                    display: 'inline-block',
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: color,
                    marginRight: 6,
                    verticalAlign: 'middle',
                  }}
                />
                <span style={{ fontSize: '0.85rem' }}>{nb.title || nb.content?.slice(0, 30) || nb.type}</span>
                {rel && (
                  <span className="muted" style={{ marginLeft: 6, fontSize: '0.7rem' }}>
                    {rel.outgoing ? '→' : '←'} {rel.type}
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 8, marginBottom: 2 }}>
      <dt className="muted" style={{ fontSize: '0.7rem', minWidth: 70, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </dt>
      <dd style={{ margin: 0, fontSize: '0.8rem' }}>{children}</dd>
    </div>
  );
}

function formatOccurred(iso: string, precision?: 'day' | 'month' | 'year' | null): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  switch (precision) {
    case 'year':
      return String(d.getUTCFullYear());
    case 'month':
      return `${d.getUTCFullYear()}-${String(d.getUTCMonth() + 1).padStart(2, '0')}`;
    default:
      return d.toLocaleDateString();
  }
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function humanize(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return 'неизвестная ошибка';
}

const panelStyle: React.CSSProperties = {
  position: 'absolute',
  top: 12,
  right: 12,
  width: 320,
  maxHeight: 'calc(100% - 24px)',
  overflowY: 'auto',
  zIndex: 10,
};

const headerStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  marginBottom: '0.5rem',
};

const closeBtnStyle: React.CSSProperties = {
  background: 'transparent',
  border: '1px solid var(--border-subtle)',
  width: 28,
  height: 28,
  borderRadius: 6,
  fontSize: '1.1rem',
  lineHeight: 1,
  cursor: 'pointer',
  padding: 0,
};

const dlStyle: React.CSSProperties = {
  margin: 0,
  padding: 0,
};

const neighborBtnStyle: React.CSSProperties = {
  width: '100%',
  textAlign: 'left',
  background: 'transparent',
  border: '1px solid var(--border-subtle)',
  borderRadius: 6,
  padding: '6px 8px',
  cursor: 'pointer',
};
