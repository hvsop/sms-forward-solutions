import { build } from 'esbuild';
await build({ entryPoints: ['hpack.js'], bundle: true, platform: 'node', format: 'esm', outfile: '.test/hpack.mjs', external: ['buffer', 'stream', 'events', 'util'], mainFields: ['browser', 'main'] });
