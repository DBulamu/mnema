# Frontend

React + Vite + TypeScript for Mnema. Types are generated from
[`../docs/openapi.yaml`](../docs/openapi.yaml) via
[`openapi-typescript`](https://github.com/openapi-ts/openapi-typescript).

## Status

Empty placeholder. Next steps:

1. `pnpm create vite . --template react-ts`
2. Add `openapi-typescript` and a generation script.
3. Auth flow (magic-link) + token storage.
4. Chat UI for the Conversation API.
5. Graph view — open spike: `vis-network` vs `Cytoscape.js` vs `D3` for
   the timeline-layout (H16). This is the largest open technical risk.
