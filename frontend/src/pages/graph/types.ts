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

// Palette tuned for the Obsidian-style charcoal canvas — saturated
// enough to read against #1a1a1a, muted enough to not clash with the
// lavender accent. One color per node type.
export const TYPE_COLOR: Record<NodeType, string> = {
  thought: '#9ca3af',
  idea: '#f59e0b',
  memory: '#a78bfa',
  dream: '#f472b6',
  emotion: '#f87171',
  task: '#34d399',
  event: '#60a5fa',
  person: '#38bdf8',
  place: '#2dd4bf',
  topic: '#c084fc',
};

export function nodeLabel(n: GraphNode): string {
  return n.title?.trim() || n.content?.slice(0, 40) || n.type;
}
