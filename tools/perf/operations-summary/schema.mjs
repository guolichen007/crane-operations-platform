// Prints the concatenated goose Up sections of deploy/postgres/migrations (00001..latest) for a scratch DB.
import { readdirSync, readFileSync } from 'node:fs';
const root = process.argv[2];
const dir = `${root}/deploy/postgres/migrations`;
for (const f of readdirSync(dir).filter((f) => /^\d{5}_.*\.sql$/.test(f)).sort()) {
  const up = readFileSync(`${dir}/${f}`, 'utf8').split('-- +goose Down')[0];
  process.stdout.write(`-- ${f}\n` + up.replace('-- +goose Up', '').replaceAll('-- +goose StatementBegin', '').replaceAll('-- +goose StatementEnd', '') + '\n');
}
