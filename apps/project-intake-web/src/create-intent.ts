// A creation intent survives refresh within the same tab. If a POST was sent
// but its response was lost, the receipt endpoint is queried before any new
// create is attempted, so one intent can never produce two projects.
export interface CreateIntent {
  actor: string;
  key: string;
  sent: boolean;
  at: number;
}
const STORAGE_KEY = 'baobei.createIntent';
const MAX_AGE = 8 * 3600_000;

type Store = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
const store = (): Store | null => {
  try {
    return sessionStorage;
  } catch {
    return null;
  }
};

export function newKey(random: () => string = () => crypto.randomUUID()) {
  return 'intent-' + random().replace(/[^A-Za-z0-9_-]/g, '');
}

export function readIntent(actor: string, s: Store | null = store(), now = Date.now()): CreateIntent | null {
  try {
    const raw = s?.getItem(STORAGE_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as CreateIntent;
    if (v.actor === actor && /^[A-Za-z0-9_-]{16,128}$/.test(v.key) && now - v.at < MAX_AGE) return v;
  } catch {
    /* Malformed storage is treated as no intent. */
  }
  clearIntent(s);
  return null;
}
export function startIntent(actor: string, s: Store | null = store(), now = Date.now()): CreateIntent {
  const v = { actor, key: newKey(), sent: false, at: now };
  try {
    s?.setItem(STORAGE_KEY, JSON.stringify(v));
  } catch {
    /* Without storage the intent still protects in-page retries. */
  }
  return v;
}
export function markSent(v: CreateIntent, s: Store | null = store()) {
  v.sent = true;
  try {
    s?.setItem(STORAGE_KEY, JSON.stringify(v));
  } catch {
    /* See startIntent. */
  }
}
export function clearIntent(s: Store | null = store()) {
  try {
    s?.removeItem(STORAGE_KEY);
  } catch {
    /* Storage is optional. */
  }
}
