let lastActionElement: HTMLElement | null = null;
export function rememberAction(element: HTMLElement) {
  lastActionElement = element;
}
export function actionInvoker() {
  return lastActionElement;
}
