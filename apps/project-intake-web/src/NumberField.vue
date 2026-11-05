<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import ActionButton from './ActionButton.vue';
import { parseNumber } from './numeric';
import type { KnowledgeState } from './model';
const props = withDefaults(
  defineProps<{
    label: string;
    unit?: string;
    name?: string;
    integer?: boolean;
    modelValue: number | null | undefined;
    state?: KnowledgeState;
    required?: boolean;
  }>(),
  { unit: '', name: undefined, integer: false, state: undefined, required: false }
);
const emit = defineEmits<{ update: [value: number | null, state: KnowledgeState] }>();
const legacy = computed(() => props.state === 'LEGACY_UNCONFIRMED' && props.modelValue === 0);
const display = () => (legacy.value || props.modelValue == null ? '' : String(props.modelValue));
const text = ref(display());
const invalid = ref(false);
watch(
  () => [props.modelValue, props.state],
  () => {
    if (invalid.value) return;
    const parsed = parseNumber(text.value, props.integer);
    if (parsed !== (props.modelValue ?? null) || legacy.value) text.value = display();
  }
);
function onInput(e: Event) {
  text.value = (e.target as HTMLInputElement).value;
  const v = parseNumber(text.value, props.integer);
  invalid.value = v === undefined;
  if (v === undefined) return;
  // Clearing the box does not silently drop a legacy value; use the explicit action.
  if (v === null && legacy.value) return;
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
    <small v-else-if="invalid" class="field-error">请输入不小于 0 的{{ integer ? '整数' : '数字' }}</small>
    <small v-else-if="modelValue == null" class="field-hint">未知</small>
  </div>
</template>
