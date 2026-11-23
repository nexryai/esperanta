# Esperanta documentation

MDX documentation for Esperanta, built with Astro and Fumapress. The Notebook
layout keeps search, the language switch and the GitHub link in the top bar.

## Development

```sh
pnpm install --frozen-lockfile
pnpm dev
```

English: `/en/`, Japanese: `/ja/`, Simplified Chinese: `/zh-CN/`.
The root redirects to English. Search indexes and sidebars are separate for each
language; missing translations do not fall back to another language.

## Editing

Pages live in `content/<locale>/*.mdx`; `meta.json` sets their sidebar order.
Keep the same slugs and command/configuration examples in all three languages.
Use links with the current locale prefix. UI labels live in `src/i18n.ts`.

Verify behavior against `../cmd/esperanta`, `../internal/config/config.go`,
`../internal/proxy/`, `../systemd/esperanta.service`, `../packaging/` and
`../snap/` when updating the docs. Installation commands are based on
`../README.md`.

`fumadocs-core` and `fumadocs-ui` are pinned to 16.18.3 because the installed
Astro integration uses APIs removed in 16.19.0. Upgrade the integration and both
packages together, then check the generated pages.

## Checks

```sh
pnpm types:check
pnpm test
```

`pnpm test` builds the site and checks translation coverage, example parity,
internal links, language metadata, Notebook navigation and localized search data.

## Deployment

The `pages` workflow builds the documentation from the release tag (or `main`
for documentation-only pushes) and merges `docs/dist` into the same Pages
artifact as the signed APT and pacman repositories. Documentation is the site
root at `https://nexryai.github.io/esperanta/`; package paths (`pool/`, `dists/`
and `pacman/`) are unchanged.

To reproduce the Pages build locally:

```sh
DOCS_SITE_URL=https://nexryai.github.io DOCS_BASE_PATH=/esperanta pnpm test
```
