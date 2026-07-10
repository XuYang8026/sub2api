<!-- <fork:proxy-circuit-breaker> -->
<template>
  <span
    v-if="showBadge"
    class="inline-flex items-center rounded bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/40 dark:text-red-300"
    :title="titleText"
  >
    {{ t('admin.accounts.proxy_unavailable') }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useProxyHealthSnapshot } from '@/composables/useProxyHealthSnapshot'

const props = defineProps<{
  proxyId: number | null | undefined
}>()

const { t } = useI18n()
const { unhealthyProxyIds, snapshots } = useProxyHealthSnapshot()

const showBadge = computed(() => {
  if (props.proxyId == null) return false
  return unhealthyProxyIds.value.has(props.proxyId)
})

const titleText = computed(() => {
  if (props.proxyId == null) return t('admin.accounts.proxy_unavailable')
  const snap = snapshots.value.find((s) => s.proxy_id === props.proxyId)
  return snap?.last_probe_error || t('admin.accounts.proxy_unavailable')
})
</script>

<!-- </fork:proxy-circuit-breaker> -->
