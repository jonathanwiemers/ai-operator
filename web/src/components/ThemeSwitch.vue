<script setup lang="ts">
// Light, Dark or Auto (follow the system), in the sidebar footer. The choice
// goes through the controller main.ts provides (colorScheme.ts), which sets
// the `dark` class and keeps the choice in this browser.
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { inject, ref } from 'vue'
import { themeInjectionKey, themes, type Theme } from '../colorScheme'

const controller = inject(themeInjectionKey, null)
const selected = ref<Theme>(controller?.get() ?? 'auto')

const items = [
  { label: 'Light', value: 'light', icon: 'i-lucide-sun' },
  { label: 'Dark', value: 'dark', icon: 'i-lucide-moon' },
  { label: 'Auto', value: 'auto', icon: 'i-lucide-monitor' },
]

function pick(v: string | number) {
  const theme = themes.find((t) => t === v)
  if (!theme) return
  selected.value = theme
  controller?.set(theme)
}
</script>

<template>
  <!-- The rail is 248 px wide, 220 inside its footer: tighter triggers keep
       the three labels whole. -->
  <UTabs
    :model-value="selected"
    :items="items"
    variant="pill"
    size="sm"
    :content="false"
    :ui="{ list: 'w-full', trigger: 'px-2 gap-1' }"
    data-theme-switch
    @update:model-value="pick"
  />
</template>
