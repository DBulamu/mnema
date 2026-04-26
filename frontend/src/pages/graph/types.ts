export const NODE_TYPES = [
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

export type NodeType = (typeof NODE_TYPES)[number];

export type GraphNode = {
  id: string;
  type: NodeType;
  title?: string | null;
  content?: string | null;
  activation: number;
  created_at: string;
  image_url?: string | null;
};

export type GraphEdge = {
  id: string;
  source_id: string;
  target_id: string;
  type: string;
  weight: number;
};

export type GraphResp = { nodes: GraphNode[]; edges: GraphEdge[] };

// Palette tuned to read on a light background. One color per node type.
export const TYPE_COLOR: Record<NodeType, string> = {
  thought: '#6b7280',
  idea: '#f59e0b',
  memory: '#8b5cf6',
  dream: '#ec4899',
  emotion: '#ef4444',
  task: '#10b981',
  event: '#3b82f6',
  person: '#0ea5e9',
  place: '#14b8a6',
  topic: '#a855f7',
};

export function nodeLabel(n: GraphNode): string {
  return n.title?.trim() || n.content?.slice(0, 40) || n.type;
}
