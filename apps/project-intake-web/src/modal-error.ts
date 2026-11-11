import type { InjectionKey } from 'vue';

// Provided by Modal.vue: an action that fails inside a dialog reports here so the
// message is shown inside that dialog instead of the page banner behind it.
export const modalErrorKey: InjectionKey<(message: string) => void> = Symbol('modal-error');
