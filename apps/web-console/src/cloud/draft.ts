// Reusable draft-editing logic: a pinned base version, dirty detection, and a
// discard guard. Kept framework-light (plain refs + pure helpers) so it is
// unit-testable and shared by the asset editor, move, retire and Equipment editor.
import { computed, ref, type Ref } from 'vue';

/** Deep, order-stable comparison of plain JSON data (form drafts). */
export function sameData(a: unknown, b: unknown): boolean {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
}

export type DraftSession<T> = {
  /** Snapshot taken when the editor opened; never refreshed behind the user's back. */
  base: Ref<T | null>;
  /** The If-Match version captured at open — used for every submit of this draft. */
  baseVersion: Ref<number | null>;
  draft: Ref<T | null>;
  open: Ref<boolean>;
  /** True only when the draft actually differs from the base. */
  dirty: Readonly<Ref<boolean>>;
  /** Server copy fetched after a 409, shown side-by-side; null when none. */
  latest: Ref<T | null>;
  start(snapshot: T, version: number): void;
  /** Rebase onto the latest server copy (user chose to start over from it). */
  rebase(snapshot: T, version: number): void;
  close(): void;
};

export function useDraftSession<T>(): DraftSession<T> {
  const base = ref(null) as Ref<T | null>;
  const draft = ref(null) as Ref<T | null>;
  const latest = ref(null) as Ref<T | null>;
  const baseVersion = ref<number | null>(null);
  const open = ref(false);
  const dirty = computed(() => open.value && !sameData(base.value, draft.value));
  const clone = (v: T): T => JSON.parse(JSON.stringify(v)) as T;
  return {
    base,
    baseVersion,
    draft,
    open,
    dirty,
    latest,
    start(snapshot, version) {
      base.value = clone(snapshot);
      draft.value = clone(snapshot);
      baseVersion.value = version;
      latest.value = null;
      open.value = true;
    },
    rebase(snapshot, version) {
      base.value = clone(snapshot);
      draft.value = clone(snapshot);
      baseVersion.value = version;
      latest.value = null;
    },
    close() {
      open.value = false;
      base.value = null;
      draft.value = null;
      latest.value = null;
      baseVersion.value = null;
    },
  };
}

/**
 * Field-level differences between two snapshots, for the 409 comparison view.
 * Every key present in either snapshot is compared except `ignore` (e.g. the
 * version, shown separately). Labelled keys use their Chinese label; an
 * unlabelled key that differs is still reported under its key name, so a
 * background change to a field outside the form (e.g. the status) is never
 * presented as "no difference".
 */
export function fieldDiff<T extends object>(
  mine: T,
  theirs: T,
  labels: Partial<Record<keyof T, string>>,
  ignore: (keyof T)[] = [],
): { field: string; mine: unknown; theirs: unknown }[] {
  const out: { field: string; mine: unknown; theirs: unknown }[] = [];
  const keys = new Set<keyof T>([...(Object.keys(labels) as (keyof T)[]), ...(Object.keys(mine) as (keyof T)[]), ...(Object.keys(theirs) as (keyof T)[])]);
  for (const key of keys) {
    if (ignore.includes(key)) continue;
    if (!sameData(mine[key], theirs[key])) out.push({ field: labels[key] ?? String(key), mine: mine[key], theirs: theirs[key] });
  }
  return out;
}

/**
 * Decide whether closing a draft may proceed. Clean drafts close silently; dirty
 * drafts ask `confirm` (injectable for tests / custom dialogs).
 */
export function allowDiscard(isDirty: boolean, confirm: (message: string) => boolean = (m) => window.confirm(m)): boolean {
  return !isDirty || confirm('有未保存的修改，确定放弃吗？');
}

/**
 * Closing / leaving while a submit is in flight is refused outright (no prompt):
 * the outcome of the request is not yet known and the dialog must stay to show
 * it. Otherwise the usual DirtyGuard applies.
 */
export function allowClose(isDirty: boolean, isPending: boolean, confirm: (message: string) => boolean = (m) => window.confirm(m)): boolean {
  if (isPending) return false;
  return allowDiscard(isDirty, confirm);
}

/**
 * Starting over from the server's latest version replaces the user's draft. It is
 * never silent: a dirty draft requires an explicit confirmation (the caller keeps
 * a read-only copy of the discarded draft for reference).
 */
export function allowRebase(isDirty: boolean, confirm: (message: string) => boolean = (m) => window.confirm(m)): boolean {
  return !isDirty || confirm('将以服务器最新版本重新开始编辑，您当前的草稿会被替换（下方保留只读副本可复制）。确定继续吗？');
}

/** Human-readable text of a draft's changed fields, for copy / reference. */
export function draftText<T extends object>(changes: Partial<T>, labels: Partial<Record<keyof T, string>>): string {
  return (Object.keys(changes) as (keyof T)[])
    .map((k) => `${labels[k] ?? String(k)}：${changes[k] ?? '（清空）'}`)
    .join('\n');
}
