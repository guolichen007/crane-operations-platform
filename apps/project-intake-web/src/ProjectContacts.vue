<script setup lang="ts">
// V1.1 R5: customers of a project and the contacts linked to it. Customer
// links open only for customers the viewer may read; linking a contact is the
// project owner's action (server re-checks owner, version and contact team).
import { ref, onMounted } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { useApp } from './store';
import { recovery } from './reasons';
import type { Project } from './model';
import ActionButton from './ActionButton.vue';

const props = defineProps<{ project: Project }>();
const emit = defineEmits<{ changed: [] }>();
const app = useApp(),
  router = useRouter(),
  route = useRoute();
interface Linked { id: string; name: string; title: string; department: string; phone: string }
interface Cust { id?: string; name: string; role: string; contacts?: { id: string; name: string; title: string }[] }
const items = ref<Linked[]>([]),
  customers = ref<Cust[]>([]),
  failure = ref(''),
  choice = ref('');
const roles: Record<string, string> = { END_CUSTOMER: '终端客户', CRANE_MANUFACTURER: '行车厂', DESIGN_INSTITUTE: '设计院', EPC: '总包', SYSTEM_INTEGRATOR: '集成商', AGENT: '代理商', OTHER: '其他' };
async function load() {
  failure.value = '';
  try {
    const b = await app.api<{ items: Linked[]; customers: Cust[] }>(`/api/v1/projects/${props.project.id}/contacts`);
    items.value = b.items;
    customers.value = b.customers;
  } catch (e) {
    failure.value = recovery(e).message;
  }
}
onMounted(load);
// Owner of the project (sales or supervisor), any open status: same rule as the server.
const canLink = () => props.project.ownerSubject === app.user?.subject && (app.user?.role === 'baobei_sales' || app.user?.role === 'baobei_manager');
async function link(contactId: string, unlink = false) {
  if (!contactId) return;
  try {
    const b = await app.api<{ items: Linked[]; customers: Cust[] }>(`/api/v1/projects/${props.project.id}/contacts`, 'POST', { contactId, version: props.project.version, unlink });
    items.value = b.items;
    customers.value = b.customers;
    choice.value = '';
    app.toast(unlink ? '已取消关联联系人' : '已关联联系人');
  } catch (e) {
    const r = recovery(e);
    if (r.kind === 'conflict') emit('changed');
    throw new Error(r.message);
  }
}
const here = () => router.resolve({ path: route.path, query: route.query }).fullPath;
</script>
<template>
  <section class="project-contacts" aria-label="客户与联系人">
    <h3>客户与联系人</h3>
    <p v-if="failure" class="form-alert" role="alert">{{ failure }}</p>
    <ul class="plain-list">
      <li v-for="c in customers" :key="c.role + c.name">
        <span class="muted">{{ roles[c.role] || c.role }}：</span>
        <ActionButton v-if="c.id" action-id="customer.open" class="link-button" :run="() => router.push({ path: '/customers/' + c.id, query: { from: here() } })">{{ c.name }}</ActionButton>
        <span v-else>{{ c.name }}</span>
      </li>
    </ul>
    <p v-if="!items.length" class="muted">尚未关联联系人。</p>
    <ul class="contact-list">
      <li v-for="c in items" :key="c.id">
        <div><strong>{{ c.name }}</strong><span class="muted"> {{ [c.title, c.department].filter(Boolean).join(' · ') }} {{ c.phone }}</span></div>
        <ActionButton v-if="canLink()" action-id="project-contact.unlink" :run="() => link(c.id, true)">取消关联</ActionButton>
      </li>
    </ul>
    <div v-if="canLink() && customers.some((c) => c.contacts?.length)" class="contact-link">
      <select v-model="choice" aria-label="选择要关联的联系人">
        <option value="">选择本团队联系人</option>
        <template v-for="c in customers" :key="c.name">
          <option v-for="x in c.contacts || []" :key="x.id" :value="x.id" :disabled="items.some((i) => i.id === x.id)">{{ c.name }} · {{ x.name }}{{ x.title ? '（' + x.title + '）' : '' }}</option>
        </template>
      </select>
      <ActionButton action-id="project-contact.link" :disabled="!choice" reason="请先选择联系人" :run="() => link(choice)">关联</ActionButton>
    </div>
  </section>
</template>
