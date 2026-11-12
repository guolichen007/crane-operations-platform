import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const sample = {
  userId: 'anonymous-user', displayName: 'Anonymous Viewer',
  projects: [{ projectId: 'P001', projectName: 'Project-A', role: 'viewer' }],
  csrfToken: 'anonymous-session-bound-csrf-token-for-test',
  expiresAt: '2099-01-01T00:00:00Z', idleExpiresAt: '2099-01-01T00:00:00Z',
};
beforeEach(() => {
  vi.resetModules();
  vi.stubEnv('VITE_AUTH_MODE', 'oidc');
  vi.stubGlobal('window', new EventTarget());
  vi.stubGlobal('localStorage', { getItem: vi.fn(), setItem: vi.fn() });
  vi.stubGlobal('sessionStorage', { getItem: vi.fn(), setItem: vi.fn() });
});
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

it('queries Cookie session, keeps only CSRF metadata in memory and scopes LIVE by project', async () => {
  const fetcher = vi.fn<typeof fetch>().mockResolvedValue(Response.json(sample));
  vi.stubGlobal('fetch', fetcher);
  const session = await import('../src/cloud/session');
  const [first, second] = await Promise.all([session.queryWebSession(), session.queryWebSession()]);
  expect(first).toEqual(sample); expect(second).toEqual(sample); expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]?.[0]).toBe('/auth/session');
  expect(await session.csrfHeader()).toEqual({ 'X-CSRF-Token': sample.csrfToken });
  expect(session.canWatchProject('P001')).toBe(false);
  expect(session.canWatchProject('P002')).toBe(false);
  expect(localStorage.setItem).not.toHaveBeenCalled(); expect(sessionStorage.setItem).not.toHaveBeenCalled();
  session.webSession.value!.projects[0]!.role = 'operator';
  expect(session.canWatchProject('P001')).toBe(true); expect(session.canWatchProject('P002')).toBe(false);
});

it('401 clears login state; 403 reports insufficient permission without a development prompt', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 401 })));
  const session = await import('../src/cloud/session');
  expect(await session.queryWebSession()).toBeNull();
  await expect(session.csrfHeader()).rejects.toThrow('请登录');
  const api = await import('../src/cloud/api');
  vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 403 })));
  await expect(api.listProjects()).rejects.toThrow('权限不足');
  vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 401 })));
  await expect(api.listProjects()).rejects.toThrow('请登录');
});

it('unsafe LIVE and logout requests use a session-bound header and never send Bearer credentials', async () => {
  const fetcher = vi.fn<typeof fetch>(async input => String(input) === '/auth/session' ? Response.json(sample) : new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetcher);
  const session = await import('../src/cloud/session');
  const api = await import('../src/cloud/api');
  await session.queryWebSession();
  await api.closeLive('anonymous-viewer');
  const close = fetcher.mock.calls.find(call => String(call[0]).includes('/live-sessions/'));
  expect(close?.[1]).toMatchObject({ method: 'DELETE', headers: { 'X-CSRF-Token': sample.csrfToken }, credentials: 'same-origin' });
  await session.logoutWebSession();
  expect(fetcher.mock.calls.some(call => call[0] === '/auth/logout')).toBe(true);
  expect(session.webSession.value).toBeNull();
  expect(JSON.stringify(fetcher.mock.calls)).not.toContain('Bearer');
});

it('an in-flight session query cannot restore identity after logout or expiry', async () => {
  let complete!: (value: Response) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { complete = resolve; })));
  const session = await import('../src/cloud/session');
  const query = session.queryWebSession();
  session.clearWebSession();
  complete(Response.json(sample));
  expect(await query).toBeNull();
  expect(session.webSession.value).toBeNull();
});

it('Q4-QA-R1: a different account or changed grants is a new data scope (one event per change, none for the same scope)', async () => {
  let reply: unknown = sample;
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async () => Response.json(reply)));
  const session = await import('../src/cloud/session');
  const events: boolean[] = [];
  window.addEventListener('cloud-identity-changed', (e) => events.push((e as CustomEvent<{ authenticated: boolean }>).detail.authenticated));
  await session.queryWebSession(); // first login in this SPA lifetime: nothing to drop
  await session.queryWebSession(); // same user, same grants (idle re-check): nothing to drop
  expect(events).toEqual([]);
  const g0 = session.scopeGeneration;
  reply = { ...sample, projects: [{ ...sample.projects[0], role: 'operator' }] }; // role change
  await session.queryWebSession();
  reply = { ...sample, userId: 'other-user' }; // another account
  await session.queryWebSession();
  session.clearWebSession(); // logout
  expect(events).toEqual([true, true, false]);
  expect(session.scopeGeneration).toBe(g0 + 3);
  expect(session.sessionScopeKey({ ...sample, projects: [...sample.projects].reverse() })).toBe(session.sessionScopeKey(sample));
});
