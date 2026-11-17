// V1.1 R7-D2 (AC-13): remember the window scroll position of a list URL when
// leaving it, and restore it once that list has loaded again (in-app return
// button or browser back). In memory only; keyed by the full path, so a
// different filter, page or sort starts at the top.
const positions = new Map<string, number>();
const LIMIT = 50;

export function rememberScroll(fullPath: string, y = window.scrollY) {
  positions.delete(fullPath);
  positions.set(fullPath, Math.max(0, Math.round(y)));
  while (positions.size > LIMIT) positions.delete(positions.keys().next().value as string);
}

/** Restores (once) the position remembered for fullPath; returns whether it did. */
export function restoreScroll(fullPath: string): boolean {
  const y = positions.get(fullPath);
  if (y === undefined) return false;
  positions.delete(fullPath);
  window.scrollTo(0, y);
  return true;
}

export function rememberedScroll(fullPath: string) {
  return positions.get(fullPath);
}
