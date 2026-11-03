import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { filterProjects } from '../data';
import { sceneProvider } from '../scene/provider';
import type { DeviceStatus, SourceFilter } from '../data/types';
export const useOperations = defineStore('operations', () => {
  const source = ref<SourceFilter>('all'),
    status = ref<DeviceStatus | 'all' | 'planned'>('all'),
    selectedId = ref<string | null>(null),
    selectedProjectId = ref<string | null>(null);
  const projects = sceneProvider.projects;
  const devices = sceneProvider.devices;
  const selected = computed(() => devices.value.find((d) => d.deviceId === selectedId.value));
  const selectedProject = computed(() => projects.value.find((p) => p.id === selectedProjectId.value));
  const visibleProjects = computed(() =>
    filterProjects(source.value, projects.value).filter(
      (p) =>
        status.value === 'all' ||
        (status.value === 'planned'
          ? p.dataSource === 'real'
          : devices.value.some((d) => d.id === p.id && d.status === status.value))
    )
  );
  const visibleDevices = computed(() =>
    devices.value.filter(
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
