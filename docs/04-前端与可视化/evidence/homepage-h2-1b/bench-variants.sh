# usage: bench2.sh <db> <label> ; variants A C E × index none/devobs/obs × users; PG-cold (restart) single run + 7-run warm wall median
source /tmp/cc/env17.sh; export PGBIN
DB=$1; L=$2
idx() { psqlq -d $DB -qc "DROP INDEX IF EXISTS alarms_dev_obs; DROP INDEX IF EXISTS alarms_obs" >/dev/null 2>&1
  case $1 in devobs) psqlq -d $DB -qc "CREATE INDEX alarms_dev_obs ON alarms(device_id, observed_at DESC)";; obs) psqlq -d $DB -qc "CREATE INDEX alarms_obs ON alarms(observed_at DESC)";; esac
  psqlq -d $DB -qc "VACUUM ANALYZE alarms" >/dev/null; }
for ix in none devobs obs; do
  idx $ix
  sz=$(psqlq -d $DB -Atc "SELECT coalesce(pg_size_pretty(sum(pg_relation_size(indexrelid))),'0') FROM pg_index WHERE indrelid='alarms'::regclass AND indexrelid::regclass::text <> 'alarms_pkey'")
  for v in A C E; do for u in u-p1 u-p20 u-p200; do
    pgrestart >/dev/null
    cold=$(node /tmp/cc/h2/analyse.mjs $DB /tmp/cc/h2/v/$v.sql $u x | python3 -c "import sys,json;d=json.load(sys.stdin);print('%.1fms/read=%d/%s'%(d['exec_ms'],d['shared_read'],d['alarm_scans'][0].split(' removed')[0].replace(' ','_')))")
    warm=$(bash /tmp/cc/h2/wall.sh $DB $v $u)
    echo "$L ix=$ix($sz) $v $u cold[$cold] warm[$warm]"
  done; done
done
