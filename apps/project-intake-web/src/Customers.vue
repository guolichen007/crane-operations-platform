<script setup lang="ts">
// V1.1 R5 (B2) customer center: the team's customers (shared master record,
// team-private profile and contacts), the projects the viewer may read, and
// contacts. Every list and detail is decided by the server; a customer the
// viewer cannot read is "not found", never a hint about another team.
import { ref, computed, watch, onMounted } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { Search, ArrowUpRight, Plus } from '@lucide/vue';
import { useApp } from './store';
import { statuses, stages, when, type ActionHint } from './model';
import { recovery } from './reasons';
import ActionButton from './ActionButton.vue';
import Modal from './Modal.vue';

interface Profile { teamId: string; teamName: string; industry: string; region: string; customerType: string; notes: string; version: number; updatedAt: string }
interface Contact { id: string; name: string; title: string; department: string; phone: string; notes: string; teamId: string; version: number; archivedAt: string | null; canEdit: boolean }
interface Item { id: string; name: string; creditCode?: string; profile: Profile | null; projectCount: number; contactCount: number; lastProjectAt: string | null }
interface Detail extends Item {
  profiles?: Profile[];
  projects: { id: string; registrationNo: string; projectName: string; role: string; salesStage: string; intakeStatus: string; ownerName: string; updatedAt: string }[];
  contacts: Contact[];
  actions: Record<string, ActionHint>;
  history: { action: string; actorName: string; details: Record<string, unknown>; at: string }[];
}
const app = useApp(),
  router = useRouter(),
  route = useRoute();
const types: Record<string, string> = { '': '未分类', END_USER: '终端用户', EQUIPMENT_MAKER: '设备厂', DESIGN_INSTITUTE: '设计院', EPC: '总包', INTEGRATOR: '集成商', AGENT: '代理商', OTHER: '其他' };
const roles: Record<string, string> = { END_CUSTOMER: '终端客户', CRANE_MANUFACTURER: '行车厂', DESIGN_INSTITUTE: '设计院', EPC: '总包', SYSTEM_INTEGRATOR: '集成商', AGENT: '代理商', OTHER: '其他' };
const events: Record<string, string> = { 'customer.create': '建立客户资料', 'customer.edit': '更新客户资料', 'customer.credit-code': '更新信用代码', 'contact.create': '新增联系人', 'contact.edit': '修改联系人', 'contact.archive': '停用联系人', 'contact.restore': '恢复联系人', 'project-contact.link': '关联项目联系人', 'project-contact.unlink': '取消项目联系人' };
const detailId = computed(() => (typeof route.params.id === 'string' ? route.params.id : ''));
const q = ref(String(route.query.q || '')),
  page = ref(1),
  items = ref<Item[]>([]),
  total = ref(0),
  loading = ref(false),
  failure = ref(''),
  detail = ref<Detail | null>(null);
async function loadList() {
  loading.value = true;
  failure.value = '';
  try {
    const params = new URLSearchParams({ page: String(page.value), pageSize: '20' });
    if (q.value.trim()) params.set('q', q.value.trim());
    const b = await app.api<{ items: Item[]; total: number }>('/api/v1/customers?' + params);
    items.value = b.items;
    total.value = b.total;
  } catch (e) {
    failure.value = recovery(e).message;
  } finally {
    loading.value = false;
  }
}
async function loadDetail() {
  failure.value = '';
  try {
    detail.value = await app.api<Detail>('/api/v1/customers/' + detailId.value);
  } catch (e) {
    detail.value = null;
    failure.value = recovery(e).message;
  }
}
const load = () => (detailId.value ? loadDetail() : loadList());
let timer: ReturnType<typeof setTimeout>;
watch(q, () => {
  page.value = 1;
  clearTimeout(timer);
  timer = setTimeout(() => {
    void router.replace({ query: q.value.trim() ? { q: q.value.trim() } : {} });
    void loadList();
  }, 250);
});
watch(page, () => void loadList());
watch(detailId, () => void load());
onMounted(load);

// Create a customer (team profile). A similar name in the viewer's range is
// offered first; one outside it is only a fixed notice.
const createOpen = ref(false),
  form = ref({ name: '', industry: '', region: '', customerType: '', notes: '' }),
  formError = ref(''),
  similar = ref<{ items: Item[]; restrictedRelated: { message: string } | null } | null>(null);
let simTimer: ReturnType<typeof setTimeout>;
watch(() => form.value.name, (name) => {
  clearTimeout(simTimer);
  similar.value = null;
  if (name.trim().length < 2) return;
  simTimer = setTimeout(async () => {
    try {
      similar.value = await app.api('/api/v1/customers/similar?name=' + encodeURIComponent(name.trim()));
    } catch {
      similar.value = null;
    }
  }, 300);
});
async function create() {
  formError.value = form.value.name.trim() ? '' : '请填写客户名称';
  if (formError.value) return;
  const b = await app.api<{ customer: Detail; existed: boolean }>('/api/v1/customers', 'POST', form.value);
  createOpen.value = false;
  app.toast(b.existed ? '本团队已维护该客户，已打开' : '客户已建立');
  await router.push('/customers/' + b.customer.id);
}
// Profile edit (team), contact create/edit/archive (team).
const editOpen = ref(false),
  edit = ref({ industry: '', region: '', customerType: '', notes: '' });
function openEdit() {
  const p = detail.value?.profile;
  edit.value = { industry: p?.industry ?? '', region: p?.region ?? '', customerType: p?.customerType ?? '', notes: p?.notes ?? '' };
  editOpen.value = true;
}
async function saveEdit() {
  if (!detail.value) return;
  try {
    detail.value = await app.api<Detail>('/api/v1/customers/' + detail.value.id, 'PATCH', { ...edit.value, version: detail.value.profile?.version ?? 0 });
  } catch (e) {
    const r = recovery(e);
    if (r.kind === 'conflict') await loadDetail();
    throw new Error(r.message);
  }
  editOpen.value = false;
  app.toast('客户资料已更新，操作已记录');
}
const contactOpen = ref(false),
  contactForm = ref({ id: '', name: '', title: '', department: '', phone: '', notes: '', version: 0 }),
  contactError = ref('');
function openContact(c?: Contact) {
  contactError.value = '';
  contactForm.value = c ? { id: c.id, name: c.name, title: c.title, department: c.department, phone: c.phone, notes: c.notes, version: c.version } : { id: '', name: '', title: '', department: '', phone: '', notes: '', version: 0 };
  contactOpen.value = true;
}
async function saveContact() {
  if (!detail.value) return;
  contactError.value = contactForm.value.name.trim() ? '' : '请填写联系人姓名';
  if (contactError.value) return;
  const f = contactForm.value;
  try {
    detail.value = f.id
      ? await app.api<Detail>('/api/v1/contacts/' + f.id, 'PATCH', { name: f.name, title: f.title, department: f.department, phone: f.phone, notes: f.notes, version: f.version })
      : await app.api<Detail>('/api/v1/customers/' + detail.value.id + '/contacts', 'POST', { name: f.name, title: f.title, department: f.department, phone: f.phone, notes: f.notes });
  } catch (e) {
    const r = recovery(e);
    if (r.kind === 'conflict') await loadDetail();
    throw new Error(r.message);
  }
  contactOpen.value = false;
  app.toast('联系人已保存');
}
async function toggleContact(c: Contact) {
  detail.value = await app.api<Detail>(`/api/v1/contacts/${c.id}/${c.archivedAt ? 'restore' : 'archive'}`, 'POST', { name: c.name, version: c.version, reason: c.archivedAt ? '恢复联系人' : '停用联系人' });
  app.toast(c.archivedAt ? '联系人已恢复' : '联系人已停用，历史保留');
}
const here = () => router.resolve({ path: route.path, query: route.query }).fullPath;
</script>
<template>
  <template v-if="!detailId">
    <div class="page-heading">
      <div>
        <h1>客户中心 <small>{{ total }}</small></h1>
        <p class="muted">本团队维护的客户，以及你可查看的项目涉及的客户。客户名称是共享主档；行业、备注和联系人只属于本团队。</p>
      </div>
      <div class="actions">
        <ActionButton v-if="app.user?.role === 'baobei_sales' || app.user?.role === 'baobei_manager'" action-id="customer.create" primary :run="() => { form = { name: '', industry: '', region: '', customerType: '', notes: '' }; createOpen = true; }"><Plus :size="15" aria-hidden="true" />新建客户</ActionButton>
      </div>
    </div>
    <div class="filterbar">
      <label class="search-field"><Search :size="16" aria-hidden="true" /><input v-model="q" aria-label="搜索客户" placeholder="客户名称" /></label>
    </div>
    <p v-if="loading" class="muted" role="status">正在加载客户…</p>
    <div v-if="failure" class="form-alert" role="alert">{{ failure }} <ActionButton action-id="customer.reload" :run="load">重新加载</ActionButton></div>
    <ul v-else class="customer-list">
      <li v-for="c in items" :key="c.id">
        <ActionButton action-id="customer.open" class="customer-card" :run="() => router.push({ path: '/customers/' + c.id, query: { from: here() } })">
          <strong class="clamp">{{ c.name }}</strong>
          <span class="muted">{{ c.profile ? [types[c.profile.customerType], c.profile.industry, c.profile.region].filter(Boolean).join(' · ') || '资料待补充' : '本团队尚未建立资料' }}</span>
          <span class="customer-meta">项目 {{ c.projectCount }} · 联系人 {{ c.contactCount }}<template v-if="c.lastProjectAt"> · 最近更新 {{ when(c.lastProjectAt) }}</template></span>
        </ActionButton>
      </li>
    </ul>
    <div v-if="!loading && !failure && !items.length" class="empty"><h2>{{ q ? '没有符合条件的客户' : '还没有客户' }}</h2><p class="muted">新建报备填写终端客户后，客户会出现在这里。</p></div>
    <div v-if="total > 20" class="pagination">
      <span class="muted">共 {{ total }} 个 · 第 {{ page }} 页</span><ActionButton action-id="project.page-prev" :disabled="page === 1" reason="已经是第一页" :run="() => { page--; }">上一页</ActionButton><ActionButton action-id="project.page-next" :disabled="page * 20 >= total" reason="没有下一页" :run="() => { page++; }">下一页</ActionButton>
    </div>
  </template>

  <template v-else>
    <ActionButton action-id="nav.open" class="back-link" :run="() => router.push(typeof route.query.from === 'string' && route.query.from.startsWith('/customers') ? route.query.from : '/customers')">返回客户中心</ActionButton>
    <div v-if="failure" class="form-alert" role="alert">{{ failure }} <ActionButton action-id="customer.reload" :run="load">重新加载</ActionButton></div>
    <template v-if="detail">
      <div class="page-heading">
        <div>
          <h1>{{ detail.name }}</h1>
          <p class="muted">
            {{ detail.profile ? [types[detail.profile.customerType], detail.profile.industry, detail.profile.region].filter(Boolean).join(' · ') || '资料待补充' : '本团队尚未建立资料' }}
            <template v-if="detail.creditCode"> · 信用代码 {{ detail.creditCode }}</template>
          </p>
        </div>
        <div class="actions">
          <ActionButton v-if="detail.actions['customer.edit']?.allowed" action-id="customer.edit" :run="openEdit">编辑客户资料</ActionButton>
          <ActionButton v-if="detail.actions['contact.create']?.allowed" action-id="contact.create" primary :run="() => openContact()"><Plus :size="15" aria-hidden="true" />新增联系人</ActionButton>
        </div>
      </div>
      <p v-if="detail.profile?.notes" class="note">{{ detail.profile.notes }}</p>
      <section v-if="detail.profiles?.length" class="panel" aria-label="各团队资料">
        <h2>各团队资料</h2>
        <ul class="plain-list"><li v-for="p in detail.profiles" :key="p.teamId"><strong>{{ p.teamName }}</strong> · {{ [types[p.customerType], p.industry, p.region].filter(Boolean).join(' · ') || '资料待补充' }}</li></ul>
      </section>
      <section class="panel" aria-label="相关项目">
        <h2>相关项目 <small class="muted">{{ detail.projects.length }}</small></h2>
        <p v-if="!detail.projects.length" class="muted">你的范围内没有涉及该客户的项目。</p>
        <ul class="plain-list">
          <li v-for="p in detail.projects" :key="p.id">
            <ActionButton action-id="project.detail" class="link-button" :run="() => router.push({ path: '/projects/' + p.id, query: { from: here() } })">{{ p.projectName || p.registrationNo }} <ArrowUpRight :size="13" aria-hidden="true" /></ActionButton>
            <span class="muted"> {{ p.registrationNo }} · {{ roles[p.role] || p.role }} · {{ stages[p.salesStage] }} · {{ statuses[p.intakeStatus] }} · 负责人 {{ p.ownerName || '姓名待核对' }}</span>
          </li>
        </ul>
      </section>
      <section class="panel" aria-label="联系人">
        <h2>联系人 <small class="muted">{{ detail.contacts.filter((c) => !c.archivedAt).length }}</small></h2>
        <p v-if="!detail.contacts.length" class="muted">本团队还没有该客户的联系人。</p>
        <ul class="contact-list">
          <li v-for="c in detail.contacts" :key="c.id" :class="{ archived: c.archivedAt }">
            <div><strong>{{ c.name }}</strong><span class="muted"> {{ [c.title, c.department].filter(Boolean).join(' · ') }}</span><span v-if="c.archivedAt" class="badge">已停用</span></div>
            <div class="muted">{{ c.phone || '未留电话' }}<template v-if="c.notes"> · {{ c.notes }}</template></div>
            <div v-if="c.canEdit" class="actions">
              <ActionButton action-id="contact.edit" :run="() => openContact(c)">编辑</ActionButton>
              <ActionButton action-id="contact.archive" :run="() => toggleContact(c)">{{ c.archivedAt ? '恢复' : '停用' }}</ActionButton>
            </div>
          </li>
        </ul>
      </section>
      <section class="panel" aria-label="客户资料记录">
        <h2>资料记录</h2>
        <p v-if="!detail.history.length" class="muted">暂无记录</p>
        <ol class="plain-list"><li v-for="(h, i) in detail.history" :key="i"><strong>{{ events[h.action] || h.action }}</strong> <span class="muted">{{ h.actorName }} · {{ when(h.at) }}</span></li></ol>
      </section>
    </template>
  </template>

  <Modal :open="createOpen" title="新建客户" description="客户名称为共享主档；行业、地区、备注和联系人只属于本团队。" @update:open="(v) => (createOpen = v)">
    <form class="staff-form" novalidate @submit.prevent="create">
      <label :class="{ invalid: formError }">客户名称 <span class="req" aria-hidden="true">*</span><input v-model="form.name" name="customerName" maxlength="200" aria-required="true" /><small v-if="formError" class="field-error">{{ formError }}</small></label>
      <div v-if="similar && (similar.items.length || similar.restrictedRelated)" class="duplicate-hint warning" role="status">
        <template v-if="similar.items.length">
          名称相近的客户：
          <ActionButton v-for="s in similar.items" :key="s.id" action-id="customer.open" class="link-button" :run="() => { createOpen = false; return router.push('/customers/' + s.id); }">{{ s.name }}</ActionButton>
        </template>
        <p v-if="similar.restrictedRelated" data-testid="customer-restricted">{{ similar.restrictedRelated.message }}</p>
      </div>
      <label>客户类型<select v-model="form.customerType" name="customerType"><option v-for="(l, k) in types" :key="k" :value="k">{{ l }}</option></select></label>
      <label>行业<input v-model="form.industry" name="industry" maxlength="100" /></label>
      <label>地区<input v-model="form.region" name="region" maxlength="100" /></label>
      <label>备注<textarea v-model="form.notes" name="notes" maxlength="2000" rows="2"></textarea></label>
      <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { createOpen = false; }">取消</ActionButton><ActionButton action-id="customer.confirm" primary :run="create">保存</ActionButton></div>
    </form>
  </Modal>
  <Modal :open="editOpen" title="编辑客户资料" description="只修改本团队的客户资料，其他团队不可见。" @update:open="(v) => (editOpen = v)">
    <form class="staff-form" novalidate @submit.prevent="saveEdit">
      <label>客户类型<select v-model="edit.customerType" name="customerType"><option v-for="(l, k) in types" :key="k" :value="k">{{ l }}</option></select></label>
      <label>行业<input v-model="edit.industry" name="industry" maxlength="100" /></label>
      <label>地区<input v-model="edit.region" name="region" maxlength="100" /></label>
      <label>备注<textarea v-model="edit.notes" name="notes" maxlength="2000" rows="3"></textarea></label>
      <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { editOpen = false; }">取消</ActionButton><ActionButton action-id="customer.confirm" primary :run="saveEdit">保存</ActionButton></div>
    </form>
  </Modal>
  <Modal :open="contactOpen" :title="contactForm.id ? '编辑联系人' : '新增联系人'" description="联系人只在本团队可见；电话仅本团队完整显示。" @update:open="(v) => (contactOpen = v)">
    <form class="staff-form" novalidate @submit.prevent="saveContact">
      <label :class="{ invalid: contactError }">姓名 <span class="req" aria-hidden="true">*</span><input v-model="contactForm.name" name="contactName" maxlength="64" aria-required="true" /><small v-if="contactError" class="field-error">{{ contactError }}</small></label>
      <label>职务<input v-model="contactForm.title" name="contactTitle" maxlength="100" /></label>
      <label>部门<input v-model="contactForm.department" name="contactDepartment" maxlength="100" /></label>
      <label>联系电话（选填）<input v-model="contactForm.phone" name="contactPhone" maxlength="32" inputmode="tel" /></label>
      <label>备注<textarea v-model="contactForm.notes" name="contactNotes" maxlength="1000" rows="2"></textarea></label>
      <div class="actions modal-actions"><ActionButton action-id="dialog.cancel" :run="() => { contactOpen = false; }">取消</ActionButton><ActionButton action-id="contact.confirm" primary :run="saveContact">保存</ActionButton></div>
    </form>
  </Modal>
</template>
