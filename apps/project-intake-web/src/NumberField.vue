<script setup lang="ts">
import { ref, watch, computed, onMounted, onBeforeUnmount } from 'vue';
import ActionButton from './ActionButton.vue';
import { parseNumber, parseMoney } from './numeric';
import type { KnowledgeState } from './model';
const props = withDefaults(
  defineProps<{
    label: string;
    unit?: string;
    name?: string;
    integer?: boolean;
    money?: boolean;
    modelValue: number | null | undefined;
    state?: KnowledgeState;
    required?: boolean;
  }>(),
  { unit: '', name: undefined, integer: false, money: false, state: undefined, required: false }
);
// validity: the text currently in the box cannot be saved (the model keeps its
// last valid value, so the form must not be saved while invalid).
const emit = defineEmits<{ update: [value: number | null, state: KnowledgeState]; validity: [invalid: boolean] }>();
const legacy = computed(() => props.state === 'LEGACY_UNCONFIRMED' && props.modelValue === 0);
const display = () => (legacy.value || props.modelValue == null ? '' : String(props.modelValue));
const text = ref(display());
const invalid = ref(false);
const message = ref('');
function parse(t: string): number | null | undefined {
  if (!props.money) return parseNumber(t, props.integer);
  const r = parseMoney(t);
  message.value = 'error' in r ? r.error : '';
  return 'error' in r ? undefined : r.value;
}
function setInvalid(v: boolean) {
  if (invalid.value !== v) {
    invalid.value = v;
    emit('validity', v);
  }
}
// A stored value that cannot be written back exactly (e.g. a pre-R1 amount
// with more than two decimals) is shown as entered and flagged at once, so
// the user corrects it or marks it unknown instead of being blocked silently.
function revalidate() {
  setInvalid(props.modelValue != null && !legacy.value && parse(text.value) === undefined);
}
let lastSent: number | null | undefined;
watch(
  () => [props.modelValue, props.state],
  () => {
    const external = props.modelValue !== lastSent;
    if (external) {
      // Loaded, merged or set by an explicit action: show it and re-check.
      text.value = display();
      revalidate();
      return;
    }
    if (invalid.value) return;
    const parsed = parse(text.value);
    if (parsed !== (props.modelValue ?? null) || legacy.value) text.value = display();
  }
);
onMounted(revalidate);
// Leaving the step (or removing an equipment row) drops the typed text; the
// model still holds the last valid value, so the field is no longer blocking.
onBeforeUnmount(() => invalid.value && emit('validity', false));
function onInput(e: Event) {
  text.value = (e.target as HTMLInputElement).value;
  const v = parse(text.value);
  setInvalid(v === undefined);
  // Invalid text is kept on screen and never sent; the form is blocked.
  if (v === undefined) return;
  // Clearing the box does not silently drop a legacy value; use the explicit action.
  if (v === null && legacy.value) return;
  lastSent = v;
  emit('update', v, v === null ? 'UNKNOWN' : 'KNOWN');
}
</script>
<template>
  <div class="num-field">
    <label>{{ label }}{{ required ? ' *' : '' }}<span class="num-input"><input
      :name="name"
      :value="text"
      inputmode="decimal"
      autocomplete="off"
      :aria-invalid="invalid"
      :placeholder="legacy ? '历史值 0 待确认' : '未知可留空'"
      @input="onInput"
    /><span v-if="unit" class="unit" aria-hidden="true">{{ unit }}</span></span></label>
    <div v-if="legacy" class="legacy-zero" role="note">
      <span>历史记录为 0，尚未确认是否真实</span>
      <ActionButton action-id="number.confirm-zero" :run="() => emit('update', 0, 'KNOWN')">确认为 0</ActionButton>
      <ActionButton action-id="number.set-unknown" :run="() => emit('update', null, 'UNKNOWN')">改为未知</ActionButton>
    </div>
    <small v-else-if="invalid" class="field-error" role="alert">{{ money && message ? message : '请输入不小于 0 的' + (integer ? '整数' : '数字') }}</small>
    <small v-else-if="modelValue == null" class="field-hint">未知</small>
  </div>
</template>
