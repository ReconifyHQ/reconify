# Reconify Docs

This directory contains the Reconify documentation website. It is a Fumadocs app built on Next.js and reads Markdown/MDX from `content/docs`.

Reconify is an open-source reconciliation engine for financial data. It parses CSV, JSON/NDJSON, and XLSX files from multiple sources, normalizes transactions, and matches them using configurable rules.

Run the development server:

```bash
npm install
npm run dev
```

Open http://localhost:3000/docs.

## Structure

| Path | Purpose |
|---|---|
| `content/docs` | Documentation pages in Markdown/MDX |
| `app/docs` | Fumadocs routes and layout |
| `app/api/search/route.ts` | Local document search route |
| `lib/source.ts` | Content source adapter |
