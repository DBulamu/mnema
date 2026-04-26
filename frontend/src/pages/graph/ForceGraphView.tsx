import { useEffect, useMemo, useRef, useState } from 'react';
import ForceGraph2D, { type ForceGraphMethods, type NodeObject } from 'react-force-graph-2d';
import { TYPE_COLOR, nodeLabel } from './types';
import type { GraphResp } from './types';

type FGNode = NodeObject & {
  id: string;
  type: keyof typeof TYPE_COLOR;
  label: string;
  degree: number;
  imageUrl: string | null;
};

type FGLink = {
  source: string;
  target: string;
  type: string;
};

type Props = {
  data: GraphResp;
  width: number;
  height: number;
};

// Per-session image cache: forces only one fetch per URL across renders
// and tells the canvas-painter whether the bitmap is ready yet.
type ImgState = { img: HTMLImageElement; loaded: boolean };
const imgCache = new Map<string, ImgState>();

function getImage(url: string, onLoad: () => void): ImgState {
  let entry = imgCache.get(url);
  if (entry) return entry;
  const img = new Image();
  img.crossOrigin = 'anonymous';
  entry = { img, loaded: false };
  imgCache.set(url, entry);
  img.onload = () => {
    entry!.loaded = true;
    onLoad();
  };
  img.onerror = () => {
    // Mark as failed by leaving loaded=false and clearing src so we
    // don't keep painting an empty bitmap. Fallback (colored circle)
    // takes over automatically.
    imgCache.delete(url);
  };
  img.src = url;
  return entry;
}

export function ForceGraphView({ data, width, height }: Props) {
  const fgRef = useRef<ForceGraphMethods<FGNode, FGLink> | undefined>(undefined);
  const [hoverId, setHoverId] = useState<string | null>(null);
  // Trigger a redraw when an image finishes loading mid-simulation.
  const [, setImgTick] = useState(0);
  const bumpImgTick = () => setImgTick((n) => n + 1);

  const graph = useMemo(() => {
    const degree = new Map<string, number>();
    for (const e of data.edges) {
      degree.set(e.source_id, (degree.get(e.source_id) ?? 0) + 1);
      degree.set(e.target_id, (degree.get(e.target_id) ?? 0) + 1);
    }
    const nodes: FGNode[] = data.nodes.map((n) => ({
      id: n.id,
      type: n.type,
      label: nodeLabel(n),
      degree: degree.get(n.id) ?? 0,
      imageUrl: n.image_url ?? null,
    }));
    const ids = new Set(nodes.map((n) => n.id));
    const links: FGLink[] = data.edges
      .filter((e) => ids.has(e.source_id) && ids.has(e.target_id))
      .map((e) => ({ source: e.source_id, target: e.target_id, type: e.type }));
    return { nodes, links };
  }, [data]);

  const adjacency = useMemo(() => {
    const m = new Map<string, Set<string>>();
    for (const l of graph.links) {
      if (!m.has(l.source)) m.set(l.source, new Set());
      if (!m.has(l.target)) m.set(l.target, new Set());
      m.get(l.source)!.add(l.target);
      m.get(l.target)!.add(l.source);
    }
    return m;
  }, [graph.links]);

  useEffect(() => {
    const fg = fgRef.current;
    if (!fg) return;
    // Obsidian-feel forces: looser link, weaker repel than the d3 defaults
    // so clusters spread out instead of forming one tight ball.
    fg.d3Force('charge')?.strength(-220);
    fg.d3Force('link')?.distance(80);
    // Fit the whole graph into view once after the initial spread; from
    // then on the user's pan/zoom is preserved.
    const t = setTimeout(() => fg.zoomToFit(400, 40), 600);
    return () => clearTimeout(t);
  }, [data]);

  function isHighlighted(id: string): boolean {
    if (!hoverId) return true;
    if (id === hoverId) return true;
    return adjacency.get(hoverId)?.has(id) ?? false;
  }

  return (
    <ForceGraph2D
      ref={fgRef}
      graphData={graph}
      width={width}
      height={height}
      backgroundColor="#1a1a1a"
      nodeRelSize={6}
      nodeVal={(n) => 1 + Math.sqrt(n.degree) * 1.4}
      // Obsidian-style behavior: the simulation lays the graph out, then
      // settles to rest. Drag re-energizes it (neighbors react through
      // the link force), and when the user releases, the graph drifts
      // back to a stable pose. The default d3 decay (~0.0228) reaches
      // alphaMin in ~300 ticks — without this the graph would orbit
      // forever, which felt wrong to the user.
      d3VelocityDecay={0.4}
      onNodeHover={(n) => setHoverId(n?.id as string | null ?? null)}
      onNodeClick={(n) => {
        console.log('[graph] click', n);
        // A click is a tactile signal: a tiny reheat lets neighbors
        // visibly acknowledge the interaction without a full drag.
        fgRef.current?.d3ReheatSimulation();
      }}
      onNodeDrag={() => {
        // Keep the simulation hot for the entire drag so neighbors
        // continuously follow the cursor through the link springs.
        fgRef.current?.d3ReheatSimulation();
      }}
      onNodeDragEnd={(n) => {
        // Release the node after drag — without this drag pins the node.
        n.fx = undefined;
        n.fy = undefined;
        fgRef.current?.d3ReheatSimulation();
      }}
      linkColor={(l) => {
        // Edges are light strokes against the charcoal canvas. Dim
        // unrelated edges to ~6% opacity on hover so the highlighted
        // neighborhood pops without blanking the rest of the graph.
        const src = typeof l.source === 'object' ? (l.source as FGNode).id : (l.source as string);
        const tgt = typeof l.target === 'object' ? (l.target as FGNode).id : (l.target as string);
        const dim = hoverId && !(isHighlighted(src) && isHighlighted(tgt));
        return dim ? 'rgba(220,221,222,0.06)' : 'rgba(220,221,222,0.28)';
      }}
      linkWidth={(l) => {
        const src = typeof l.source === 'object' ? (l.source as FGNode).id : (l.source as string);
        const tgt = typeof l.target === 'object' ? (l.target as FGNode).id : (l.target as string);
        return hoverId && isHighlighted(src) && isHighlighted(tgt) ? 1.6 : 0.7;
      }}
      nodeCanvasObjectMode={() => 'replace'}
      nodeCanvasObject={(node, ctx, globalScale) => {
        const n = node as FGNode;
        const radius = 8 + Math.sqrt(n.degree) * 2.2;
        const dim = hoverId && !isHighlighted(n.id);
        ctx.globalAlpha = dim ? 0.25 : 1;
        const cx = n.x ?? 0;
        const cy = n.y ?? 0;
        const color = TYPE_COLOR[n.type] ?? '#888';

        let imgEntry: ImgState | undefined;
        if (n.imageUrl) imgEntry = getImage(n.imageUrl, bumpImgTick);

        if (imgEntry?.loaded) {
          // Circular image cap: clip to a disc, draw image, then a thin
          // type-colored ring so the type is still readable at a glance.
          ctx.save();
          ctx.beginPath();
          ctx.arc(cx, cy, radius, 0, 2 * Math.PI);
          ctx.closePath();
          ctx.clip();
          const img = imgEntry.img;
          // cover-fit: scale the shorter side to the disc diameter.
          const sw = img.naturalWidth || img.width || 1;
          const sh = img.naturalHeight || img.height || 1;
          const scale = Math.max((radius * 2) / sw, (radius * 2) / sh);
          const dw = sw * scale;
          const dh = sh * scale;
          ctx.drawImage(img, cx - dw / 2, cy - dh / 2, dw, dh);
          ctx.restore();

          ctx.beginPath();
          ctx.arc(cx, cy, radius, 0, 2 * Math.PI);
          ctx.lineWidth = 2;
          ctx.strokeStyle = color;
          ctx.stroke();
        } else {
          // Plain colored circle (also the loading placeholder).
          ctx.beginPath();
          ctx.arc(cx, cy, radius, 0, 2 * Math.PI);
          ctx.fillStyle = color;
          ctx.fill();
          // Thin charcoal-tinted ring keeps the disc readable against the
          // dot-grid backdrop without reading as a "selected" outline.
          ctx.lineWidth = 1;
          ctx.strokeStyle = 'rgba(54,54,54,0.9)';
          ctx.stroke();
        }

        // Always-visible label below the node. Font size scales inversely
        // with zoom but is clamped so it never disappears or overpowers.
        const fontPx = Math.max(11, Math.min(14, 13 / Math.max(globalScale, 0.7)));
        ctx.font = `${fontPx}px Inter, -apple-system, BlinkMacSystemFont, sans-serif`;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'top';
        const text = n.label.length > 28 ? n.label.slice(0, 27) + '…' : n.label;
        const ty = cy + radius + 3;
        // Charcoal halo so the light label stays legible over edges or
        // an adjacent node — same idea as before, inverted for dark mode.
        ctx.lineWidth = 3;
        ctx.strokeStyle = 'rgba(26,26,26,0.95)';
        ctx.strokeText(text, cx, ty);
        ctx.fillStyle = '#dcddde';
        ctx.fillText(text, cx, ty);
        ctx.globalAlpha = 1;
      }}
    />
  );
}
