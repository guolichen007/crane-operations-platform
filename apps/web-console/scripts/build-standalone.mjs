import { readFile, writeFile, readdir, mkdir } from 'node:fs/promises';
import { resolve, join, relative } from 'node:path';
import { gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';
const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
process.chdir(root);
process.env.STANDALONE_HTML = '1';
const { build } = await import('vite');
await build();
const output = join(root, 'dist-offline');
let html = await readFile(join(output, 'index.html'), 'utf8');
const entry = html.match(/<script\b[^>]*\bsrc="([^"]+)"[^>]*><\/script>/);
if (!entry) throw Error('Standalone entry script missing');
const js = await readFile(resolve(output, entry[1]), 'utf8');
html = html.replace(
  entry[0],
  () => '<script type="module">' + js.replace(/<\/script/gi, '<\\/script') + '</script>'
);
for (const link of [...html.matchAll(/<link\b[^>]*\brel="stylesheet"[^>]*>/g)]) {
  const href = link[0].match(/href="([^"]+)"/)[1];
  const css = await readFile(resolve(output, href), 'utf8');
  html = html.replace(
    link[0],
    () => '<style>' + css.replace(/<\/style/gi, '<\\/style') + '</style>'
  );
}
// No preload links may point outside this one-file deliverable.
html = html.replace(/<link\b[^>]*\brel="(?:preload|modulepreload)"[^>]*>/g, '');
async function files(dir) {
  return (
    await Promise.all(
      (await readdir(dir, { withFileTypes: true })).map((e) =>
        e.isDirectory() ? files(join(dir, e.name)) : join(dir, e.name)
      )
    )
  ).flat();
}
const geoRoot = join(root, 'public', 'geo');
const geo = {};
for (const file of await files(geoRoot)) {
  if (!file.endsWith('.json')) continue;
  geo[relative(geoRoot, file).replaceAll('\\', '/')] = gzipSync(await readFile(file), {
    level: 9,
  }).toString('base64');
}
const bootstrap = String.raw`<script id="offline-map-data" type="application/json">__GEO__</script>
<script>
(()=>{
 const pack=JSON.parse(document.getElementById('offline-map-data').textContent);
 const originalFetch=window.fetch.bind(window);
 window.fetch=async(input,init)=>{
   const address=typeof input==='string'?input:input instanceof URL?input.href:input.url;
   const url=new URL(address,location.href);
   const marker=url.pathname.lastIndexOf('/geo/');
   if(marker<0)return originalFetch(input,init);
   const key=decodeURIComponent(url.pathname.slice(marker+5));
   if(!Object.hasOwn(pack,key))return new Response('Offline map not found',{status:404});
   const binary=atob(pack[key]);const bytes=Uint8Array.from(binary,c=>c.charCodeAt(0));
   const body=new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'));
   return new Response(body,{headers:{'Content-Type':'application/json; charset=utf-8'}});
 };
})();
</script>`;
// Install the in-memory map transport before Vue starts. No local server or network is involved.
html = html.replace('<head>', '<head>\n' + bootstrap.replace('__GEO__', JSON.stringify(geo)));
if (/<(?:script|link)\b[^>]*(?:src|href)="(?:\.\/assets\/|\.\/geo\/)/.test(html))
  throw Error('External build assets remain');
const release = resolve(root, '../../release');
await mkdir(release, { recursive: true });
const filename = join(release, '全国行车设备运营平台_离线版.html');
await writeFile(filename, html, 'utf8');
console.log(
  JSON.stringify({
    file: filename,
    bytes: Buffer.byteLength(html),
    embeddedMaps: Object.keys(geo).length,
  })
);
