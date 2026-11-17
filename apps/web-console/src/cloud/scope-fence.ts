import { scopeGeneration } from './session';

/**
 * R7 F-12: a write prepared under one login identity / grant scope must never be sent under
 * another. A form pins the current data scope when it opens; the submit is refused (nothing sent)
 * if the scope changed meanwhile — e.g. another account signed in in another tab and this tab
 * re-read the session in place, or the caller's grants changed.
 */
export class ScopeChangedError extends Error {
  constructor() {
    super('登录身份或授权范围已变化：此表单属于上一身份，未提交，服务器未做任何更改。请关闭后重新打开再操作。');
    this.name = 'ScopeChangedError';
  }
}

export function createScopeFence() {
  let pinned = scopeGeneration;
  return {
    /** Call when a form / confirmation opens. */
    pin() {
      pinned = scopeGeneration;
    },
    /** Run the write only if the scope is still the pinned one. */
    guard<T>(write: () => Promise<T>): Promise<T> {
      if (scopeGeneration !== pinned) return Promise.reject(new ScopeChangedError());
      return write();
    },
  };
}
