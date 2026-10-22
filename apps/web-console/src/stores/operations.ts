import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { devices, projects, filterProjects } from '../data';
import type { DeviceStatus, SourceFilter } from '../data/types';
export const useOperations = defineStore('operations', () => {
  const source = ref<SourceFilter>('all'),
    status = ref<DeviceStatus | 'all' | 'planned'>('all'),
    selectedId = ref<string | null>(null),
    selectedProjectId = ref<string | null>(null);
  const selected = computed(() => devices.find((d) => d.deviceId === selectedId.value));
  const selectedProject = computed(() => projects.find((p) => p.id === selectedProjectId.value));
  const visibleProjects = computed(() =>
    filterProjects(source.value).filter(
      (p) =>
        status.value === 'all' ||
        (status.value === 'planned'
          ? p.dataSource === 'real'
          : devices.some((d) => d.id === p.id && d.status === status.value))
    )
  );
  const visibleDevices = computed(() =>
    devices.filter(
      (d) =>
        (source.value === 'all' || d.dataSource === source.value) &&
        (status.value === 'all' ||
          (status.value === 'planned' ? d.dataSource === 'real' : d.status === status.value))
    )
  );
  function select(id: string | null) {
    selectedId.value = id;
    selectedProjectId.value = null;
  }
  function selectProject(id: string | null) {
    selectedProjectId.value = id;
    selectedId.value = null;
  }
  return {
    source,
    status,
    selectedId,
    selected,
    selectedProjectId,
    selectedProject,
    visibleProjects,
    visibleDevices,
    select,
    selectProject,
  };
});
