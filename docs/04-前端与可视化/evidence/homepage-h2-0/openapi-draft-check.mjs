// H2-0-R3: validates the OpenAPI draft block in Homepage-H2-0-运营数据契约.md §3.4 with @apidevtools/swagger-parser (OpenAPI 3.1),
// and checks every object property is required and that only scope.region is nullable.
// Run from the repo root: cp this file into tools/ (so node resolves repo node_modules) and pass the contract path.
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import SwaggerParser from '@apidevtools/swagger-parser';
const md = readFileSync(process.argv[2], 'utf8');
const block = md.slice(md.indexOf('```yaml\n/api/v1/operations/summary:') + 8, md.indexOf('```', md.indexOf('```yaml\n/api/v1/operations/summary:') + 8));
const draft = YAML.parse(block);
const doc = { openapi: '3.1.0', info: { title: 'draft', version: '0' }, paths: { '/api/v1/operations/summary': draft['/api/v1/operations/summary'] },
  components: { ...draft.components, securitySchemes: { webSession: { type: 'apiKey', in: 'cookie', name: 's' } } } };
doc.paths['/api/v1/operations/summary'].get.responses['200'].description = 'ok';
const api = await SwaggerParser.validate(structuredClone(doc));
// required ⊆ properties for every object schema; nullable only scope.region
const nulls = [];
function walk(s, path) {
  if (!s || typeof s !== 'object') return;
  if (s.type === 'object' && s.properties) {
    for (const r of s.required ?? []) if (!(r in s.properties)) throw Error(`required ${r} not in properties at ${path}`);
    for (const k of Object.keys(s.properties)) if (!(s.required ?? []).includes(k)) throw Error(`property ${k} not required at ${path}`);
  }
  if (Array.isArray(s.type) && s.type.includes('null')) nulls.push(path);
  for (const [k, v] of Object.entries(s)) walk(v, path + '/' + k);
}
walk(doc.components.schemas, '#/components/schemas');
console.log('OPENAPI_DRAFT_VALID', api.openapi, 'nullable:', nulls.join(','));
