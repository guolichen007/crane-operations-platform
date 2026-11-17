import { describe, it, expect, beforeEach, vi } from 'vitest';
import { rememberScroll, restoreScroll, rememberedScroll } from './scroll-memory';

describe('AC-13 list scroll memory', () => {
  beforeEach(() => {
    vi.stubGlobal('window', { scrollY: 0, scrollTo: vi.fn() });
  });
  it('restores once for the same full path only', () => {
    rememberScroll('/projects?page=2', 640);
    expect(restoreScroll('/projects?page=3')).toBe(false);
    expect(restoreScroll('/projects?page=2')).toBe(true);
    expect(window.scrollTo).toHaveBeenCalledWith(0, 640);
    expect(restoreScroll('/projects?page=2')).toBe(false);
  });
  it('keeps a bounded number of entries', () => {
    for (let i = 0; i < 60; i++) rememberScroll('/projects?p=' + i, i);
    expect(rememberedScroll('/projects?p=0')).toBeUndefined();
    expect(rememberedScroll('/projects?p=59')).toBe(59);
  });
});
