# Reconify Docs

This directory contains the Reconify documentation website. It is a Fumadocs app built on Next.js and reads Markdown/MDX from `content/docs`.

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
