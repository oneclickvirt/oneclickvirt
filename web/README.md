# Frontend development

Run `npm ci`, `npm run test:unit`, then `npm run build` from this directory.

The production and development builds target Chrome/Edge 89+, Firefox 89+,
and Safari 15+. NoVNC 1.7 uses native top-level await, so older browsers are
outside this build's supported baseline. ECharts 6 uses its bundled `v5` theme
to preserve the existing chart palette and layout defaults.
