<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UDropdownMenu, { type DropdownMenuItem } from '@nuxt/ui/components/DropdownMenu.vue'
import { computed } from 'vue'
import { safeURL } from '../format'

// HeaderActions is the actions part of a page header (decision 4): the
// first `shown` actions as buttons (at most one primary and one
// secondary), the rest in a ⋯ menu. A link out (an `href` key) opens a new
// tab; one whose URL is missing or unsafe is dropped, as ExtLink did. The
// default slot holds an action that is more
// than a button (a form, a button with its dialog), after the buttons and
// before ⋯; pass `shown` lower to leave it its place.
type Action = DropdownMenuItem & { label: string; href?: string | null }
const props = withDefaults(defineProps<{ actions: Action[]; shown?: number }>(), { shown: 2 })

const items = computed((): DropdownMenuItem[] =>
  props.actions.flatMap((action) => {
    if (!('href' in action)) return [action]
    const { href, ...a } = action
    const url = safeURL(href)
    return url ? [{ ...a, to: url, target: '_blank' }] : []
  }),
)
const buttons = computed(() => items.value.slice(0, props.shown))
const more = computed(() => items.value.slice(props.shown))
// tap: 44 px high on phones.
const tap = 'min-h-11 justify-center tablet:min-h-0'
</script>

<template>
  <UButton
    v-for="a in buttons"
    :key="a.label"
    color="neutral"
    variant="outline"
    size="sm"
    :class="tap"
    :label="a.label"
    :icon="a.icon"
    :to="a.to"
    :target="a.target"
    :disabled="a.disabled"
    @click="a.onSelect?.($event)"
  />
  <slot />
  <UDropdownMenu v-if="more.length" :items="more" :content="{ align: 'end' }" :ui="{ content: 'z-40', item: 'min-h-11 tablet:min-h-0' }">
    <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-ellipsis" :class="[tap, 'min-w-11 tablet:min-w-0']" aria-label="More actions" data-more-actions />
  </UDropdownMenu>
</template>
