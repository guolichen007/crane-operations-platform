import { spawn, spawnSync } from 'node:child_process';
import { createInterface } from 'node:readline';
import {
  mkdirSync,
  writeFileSync,
  readFileSync,
  existsSync,
  openSync,
  closeSync,
  createReadStream,
} from 'node:fs';
import { resolve, join } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import assert from 'node:assert/strict';
const docker = [
  ...(process.env.BAOBEI_DOCKER_CONTEXT ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT] : []),
];
// Database tools run inside the PostgreSQL container (docker exec). For
// offline verification only, BAOBEI_DB_TOOL=local runs the same client tools
// directly against a loopback server (PGHOST/PGPORT); never a remote host.
const localTools = process.env.BAOBEI_DB_TOOL === 'local';
if (localTools) assert(['127.0.0.1', 'localhost'].includes(process.env.PGHOST || ''), 'local DB tools require a loopback PGHOST');
function tool(args, options) {
  return localTools
    ? spawnSync(args[0], args.slice(1), options)
    : spawnSync('docker', [...docker, 'exec', ...(options?.interactive ? ['-i'] : []), cid, ...args], options);
}
function run(args, input, encoding = 'utf8') {
  const r = tool(args, { input, encoding, maxBuffer: 256 * 1024 * 1024 });
  if (r.status !== 0)
    throw Error('Database tool failed (no credentials or database output logged)');
  return r.stdout;
}
const composePs = () => {
  const r = spawnSync('docker', [...docker,
    'compose',
    '--env-file',
    'local-private/baobei/compose.env',
    '-f',
    'deploy/baobei/compose.yaml',
    'ps',
    '-q',
    'baobei-postgres',
  ], { encoding: 'utf8' });
  if (r.status !== 0) throw Error('Database tool failed (no credentials or database output logged)');
  return r.stdout.trim();
};
const cid = localTools ? 'local' : process.env.BAOBEI_POSTGRES_CONTAINER || composePs();
assert(/^[a-zA-Z0-9_-]+$/.test(cid));
const database = process.env.BAOBEI_BACKUP_DATABASE || 'baobei';
assert(/^baobei(?:_test)?$/.test(database), 'Only the independent Baobei DB is allowed');
const user = process.env.BAOBEI_BACKUP_USER || 'baobei';
assert(/^[a-z_][a-z0-9_]*$/.test(user));
// G4-S1A: the backup declares which environment it expects. production (the
// default, unchanged) accepts exactly the product tables and no environment
// marker; preview accepts exactly the product tables plus baobei_environment
// and requires its single row to say 'preview'. Anything else is refused.
const environment = process.env.BAOBEI_BACKUP_ENV || 'production';
assert(['production', 'preview'].includes(environment), 'BAOBEI_BACKUP_ENV must be production or preview');
const productTables = [
  'teams',
  'app_users',
  'app_sessions',
  'organizations',
  'organization_aliases',
  'contacts',
  'registration_counters',
  'intake_projects',
  'project_locations',
  'project_parties',
  'project_contacts',
  'planned_equipment',
  'project_requirements',
  'project_activities',
  'duplicate_cases',
  'approvals',
  'ownership_history',
  'conversion_jobs',
  'conversion_links',
  'audit_events',
  'reference_values',
  'baobei_migrations',
  'oidc_pending_logins',
  'staff_audit',
  'staff_sessions',
  'staff_outbox',
  'staff_handoffs',
  'project_submissions',
  'project_participations',
  'project_create_requests',
  // G4-S5-R1-E (00006): identity epoch, issuer-scoped identity links, account operations
  // (00007 adds recovery columns to account_operations, no new table).
  'identity_epoch',
  'identity_links',
  'account_operations',
].sort();
const tables = (environment === 'preview' ? [...productTables, 'baobei_environment'] : productTables).sort();
const markerSQL = `(SELECT jsonb_agg(name) FROM baobei_environment)`;
const countsSQL = `jsonb_build_object(${tables.map((t) => `'${t}',(SELECT count(*) FROM ${t})`).join(',')})`;
const metaSQL = `jsonb_build_object('counts',${countsSQL},'migrations',(SELECT json_agg(m ORDER BY name) FROM baobei_migrations m),'postgres_version',current_setting('server_version'),'extensions',(SELECT jsonb_agg(jsonb_build_object('name',extname,'version',extversion) ORDER BY extname) FROM pg_extension)${environment === 'preview' ? `,'environment_marker',${markerSQL}` : ''})`;
const query = (db, sql) =>
  run([
    'psql',
    '-X',
    '-qAt',
    '-U',
    user,
    '-d',
    db,
    '-v',
    'ON_ERROR_STOP=1',
    '-c',
    sql,
  ]).trim();
const command = process.argv[2] || 'backup';
let archive = process.argv[3];
if (command === 'backup') {
  const found = JSON.parse(
    query(
      database,
      `SELECT json_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='public'`
    )
  );
  assert.deepEqual(found, tables, 'Refuse mixed-product/unknown tables');
  assert.deepEqual(
    JSON.parse(
      query(
        database,
        `SELECT jsonb_agg(nspname ORDER BY nspname) FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname<>'information_schema'`
      )
    ),
    ['public'],
    'Refuse non-Baobei schemas'
  );
  // Environment: production has no marker table (checked by the exact table
  // list above); preview has exactly one row 'preview'.
  if (environment === 'preview')
    assert.deepEqual(JSON.parse(query(database, `SELECT ${markerSQL}`)), ['preview'], 'Refuse: not the preview database');
  const out = process.env.BAOBEI_BACKUP_DIR || resolve('local-private/baobei/backups');
  mkdirSync(out, { recursive: true, mode: 0o700 });
  archive = join(
    out,
    (environment === 'preview' ? 'baobei-preview-' : 'baobei-') + new Date().toISOString().replace(/[:.]/g, '-') + '-' + randomUUID() + '.dump'
  );
  assert(!existsSync(archive));
  const session = localTools
    ? spawn('psql', ['-X', '-qAt', '-U', user, '-d', database, '-v', 'ON_ERROR_STOP=1'], { stdio: ['pipe', 'pipe', 'pipe'] })
    : spawn(
    'docker',
    [
      ...docker,
      'exec',
      '-i',
      cid,
      'psql',
      '-X',
      '-qAt',
      '-U',
      user,
      '-d',
      database,
      '-v',
      'ON_ERROR_STOP=1',
    ],
    { stdio: ['pipe', 'pipe', 'pipe'] }
  );
  const lines = createInterface({ input: session.stdout });
  session.stdin.write(
    `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT jsonb_build_object('snapshot',pg_export_snapshot(),'metadata',${metaSQL});\n`
  );
  let snapshot;
  try {
    for await (const line of lines) {
      if (line.startsWith('{')) {
        snapshot = JSON.parse(line);
        break;
      }
    }
    assert(snapshot?.snapshot);
    const dumpFD = openSync(archive, 'wx', 0o600);
    try {
      const result = tool(
        [
          'pg_dump',
          '-U',
          user,
          '-d',
          database,
          '-Fc',
          '--no-owner',
          '--snapshot=' + snapshot.snapshot,
        ],
        { stdio: ['ignore', dumpFD, 'pipe'] }
      );
      if (result.status !== 0) throw Error('Backup dump failed');
    } finally {
      closeSync(dumpFD);
    }
    const digest = createHash('sha256');
    for await (const chunk of createReadStream(archive)) digest.update(chunk);
    const manifest = {
      format: 'baobei-backup-v1',
      database,
      environment,
      source_sha: spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).stdout.trim(),
      created_at: new Date().toISOString(),
      sha256: digest.digest('hex'),
      ...snapshot.metadata,
    };
    assert.equal(manifest.environment, environment);
    writeFileSync(archive + '.json', JSON.stringify(manifest, null, 2), {
      flag: 'wx',
      mode: 0o600,
    });
    console.log('BACKUP=PASS; private archive created');
  } finally {
    session.stdin.end('ROLLBACK;\n');
    session.stderr.resume();
    lines.close();
  }
  if (process.env.BAOBEI_BACKUP_VERIFY !== '1') process.exit(0);
} else assert.equal(command, 'restore-test');
assert(archive, 'archive path required');
const manifest = JSON.parse(readFileSync(archive + '.json', 'utf8'));
assert.equal(manifest.format, 'baobei-backup-v1');
// A backup restores only into the environment it was taken from (legacy
// manifests without the field are production backups).
assert.equal(manifest.environment || 'production', environment, 'Refuse: backup environment differs from BAOBEI_BACKUP_ENV');
const digest = createHash('sha256');
for await (const chunk of createReadStream(archive)) digest.update(chunk);
assert.equal(digest.digest('hex'), manifest.sha256);
const restored = 'baobei_restore_' + randomUUID().replaceAll('-', '');
assert(/^baobei_restore_[a-f0-9]{32}$/.test(restored));
run(['createdb', '-U', user, restored]);
try {
  const inputFD = openSync(archive, 'r');
  try {
    const result = tool(
      ['pg_restore', '-U', user, '-d', restored, '--no-owner', '--exit-on-error'],
      { stdio: [inputFD, 'pipe', 'pipe'], interactive: true }
    );
    if (result.status !== 0) throw Error('Isolated restore failed (database output suppressed)');
  } finally {
    closeSync(inputFD);
  }
  const metadata = JSON.parse(query(restored, `SELECT ${metaSQL}`));
  assert.deepEqual(metadata.counts, manifest.counts);
  assert.deepEqual(metadata.migrations, manifest.migrations);
  assert.deepEqual(metadata.extensions, manifest.extensions);
  // The restored database has exactly the expected table list and, for
  // preview, the single 'preview' marker row.
  assert.deepEqual(JSON.parse(query(restored, `SELECT json_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='public'`)), tables, 'restored table list');
  if (environment === 'preview') {
    assert.deepEqual(metadata.environment_marker, ['preview'], 'restored environment marker');
    assert.deepEqual(manifest.environment_marker, ['preview'], 'manifest environment marker');
  }
  assert.equal(
    query(restored, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND NOT convalidated`),
    '0'
  );
  assert.equal(
    query(
      restored,
      `SELECT count(*) FROM intake_projects p LEFT JOIN app_users u ON u.subject=p.created_by_subject WHERE u.subject IS NULL`
    ),
    '0'
  );
  assert.equal(
    query(
      restored,
      `SELECT count(*) FROM project_parties pp LEFT JOIN organizations o ON o.id=pp.organization_id LEFT JOIN intake_projects p ON p.id=pp.project_id WHERE o.id IS NULL OR p.id IS NULL`
    ),
    '0'
  );
  assert.equal(
    query(
      restored,
      `SELECT count(*) FROM project_activities a LEFT JOIN intake_projects p ON p.id=a.project_id WHERE p.id IS NULL`
    ),
    '0'
  );
  console.log(
    `RESTORE_TEST=PASS; environment=${environment}; ${tables.length} table counts, migrations and key relations validated`
  );
  writeFileSync(
    resolve('local-private/baobei/restore-evidence.json'),
    JSON.stringify(
      {
        backupHash: manifest.sha256,
        validatedAt: new Date().toISOString(),
        environment,
        tables: tables.length,
        result: 'PASS',
      },
      null,
      2
    )
  );
} finally {
  run(['dropdb', '-U', user, restored]);
}
