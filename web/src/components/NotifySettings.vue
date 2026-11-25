<script setup lang="ts">
// The Notifications dialog (R5 of the Desk notifications milestone), opened
// from the account menu and the Inbox through notifyDialog. App.vue mounts
// it once, on first open, and owns its back-button close. One switch turns
// notifications on in this browser (it asks for permission); one per
// category picks what notifies, usable while the main switch is off. The
// event categories need the history database: notify.history, asked while
// unknown.
import { computed, toRef, watch } from 'vue'
import UModal from '@nuxt/ui/components/Modal.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import { getStream } from '../client'
import { disableNotify, enableNotify, notify, notifyActive, notifyDialog, refreshPermission, setCategory } from '../notify'
import { notifyCategories } from '../notifyCategories'

const open = toRef(notifyDialog, 'open')

const blocked = computed(() =>
  notify.permission === 'denied' ? "Blocked in this browser's settings" : notify.permission === 'unsupported' ? 'Not supported in this browser' : undefined,
)

let asking = false
function askHistory() {
  if (notify.history !== null || asking) return
  asking = true
  getStream(new URLSearchParams({ kinds: 'autopilot', limit: '1' }))
    .then((page) => (notify.history = page.history))
    // On an error the event switches stay usable.
    .catch(() => undefined)
    .finally(() => (asking = false))
}

watch(
  open,
  (o) => {
    if (!o) return
    refreshPermission()
    askHistory()
  },
  { immediate: true },
)

function setOn(on: boolean) {
  if (on) void enableNotify()
  else disableNotify()
}

const rows = computed(() =>
  notifyCategories.map((c) => {
    const off = !!c.history && notify.history === false
    return { ...c, disabled: off, description: off ? `${c.description} Needs the history database.` : c.description }
  }),
)

// A disabled switch dims its toggle only: its label and the line that says
// why keep their contrast. The line is the switch's accessible description.
function switchUi(disabled: boolean) {
  return disabled
    ? { root: 'opacity-100', container: 'max-tablet:min-h-11 opacity-75' }
    : { container: 'max-tablet:min-h-11' }
}
</script>

<template>
  <UModal v-model:open="open" title="Notifications" description="What raises a notification while a Desk tab is open." :ui="{ overlay: 'z-40', content: 'z-40' }">
    <template #body>
      <div class="flex flex-col gap-4 text-sm" data-notify-settings>
        <USwitch
          :model-value="notifyActive()"
          label="Notifications in this browser"
          :description="blocked"
          :disabled="!!blocked"
          :ui="switchUi(!!blocked)"
          :aria-describedby="blocked ? 'notify-main-line' : undefined"
          data-notify-main
          @update:model-value="setOn"
        >
          <template v-if="blocked" #description><span id="notify-main-line">{{ blocked }}</span></template>
        </USwitch>
        <fieldset class="m-0 flex flex-col gap-3 border-t border-(--border) p-0 pt-4">
          <legend class="sr-only">Categories</legend>
          <USwitch
            v-for="c in rows"
            :key="c.category"
            :model-value="notify.categories[c.category]"
            :label="c.label"
            :description="c.description"
            :disabled="c.disabled"
            :ui="switchUi(c.disabled)"
            :aria-describedby="`notify-line-${c.category}`"
            :data-notify-category="c.category"
            @update:model-value="(v: boolean) => setCategory(c.category, v)"
          >
            <template #description><span :id="`notify-line-${c.category}`">{{ c.description }}</span></template>
          </USwitch>
        </fieldset>
      </div>
    </template>
  </UModal>
</template>
