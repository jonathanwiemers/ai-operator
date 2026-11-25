<script setup lang="ts">
// The Notifications dialog (R5 of the Desk notifications milestone), opened
// from the account menu and the Inbox through notifyDialog. App.vue mounts
// it once, on first open, and owns its back-button close. One switch turns
// notifications on in this browser (it asks for permission); one per
// category picks what notifies, usable while the main switch is off. The
// event categories need the history database: notify.history, asked while
// unknown. With Web Push on this Desk (push.ts, asked on every open) the
// main switch subscribes this device, also for while the Desk is closed;
// on iOS outside the Home Screen app a hint replaces it; with push off on
// the Desk a line says why, and notifications stay in the tab.
import { computed, ref, toRef, watch } from 'vue'
import UModal from '@nuxt/ui/components/Modal.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import { getStream, pushRoutes } from '../client'
import type { PushInfo } from '../generated/views'
import { disableNotify, enableNotify, notify, notifyActive, notifyDialog, refreshPermission, setCategory, type NotifyCategory } from '../notify'
import { notifyCategories } from '../notifyCategories'
import { pushStatus, refreshPush, subscribePush, unsubscribePush } from '../push'
import type { PushState } from '../pushSubscribe'

const open = toRef(notifyDialog, 'open')

// The push state of this device; in-tab until GET /push answers.
const pushed = ref<{ state: PushState; info: PushInfo | null }>({ state: 'inTab', info: null })
const push = computed(() => pushed.value.state === 'push')
// The last failure of turning push on or off or storing the categories.
const failed = ref<string>()
let busy = false

const blocked = computed(() =>
  notify.permission === 'denied' ? "Blocked in this browser's settings" : notify.permission === 'unsupported' ? 'Not supported in this browser' : undefined,
)
// The main switch's line: the browser's block, which disables it, or a
// failure, which leaves it usable.
const mainLine = computed(() => blocked.value ?? (push.value ? failed.value : undefined))
const mainOn = computed(() => (push.value ? notify.push : notifyActive()))

function askPush() {
  pushStatus(pushRoutes)
    .then(({ state, info, subscribed }) => {
      pushed.value = { state, info }
      if (state === 'push') notify.push = subscribed
    })
    .catch(() => undefined)
}

function message(e: unknown): string {
  return e instanceof Error && e.message ? e.message : 'Push notifications failed.'
}

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
    failed.value = undefined
    askHistory()
    askPush()
  },
  { immediate: true },
)

async function setOn(on: boolean) {
  const info = pushed.value.info
  if (!push.value || !info) {
    if (on) void enableNotify()
    else disableNotify()
    return
  }
  if (busy) return
  busy = true
  failed.value = undefined
  try {
    if (on) {
      await enableNotify()
      if (!notifyActive()) return
      await subscribePush(pushRoutes, info, notify.categories)
      notify.push = true
    } else {
      notify.push = false
      disableNotify()
      await unsubscribePush(pushRoutes)
    }
  } catch (e) {
    failed.value = message(e)
    if (on) disableNotify()
  } finally {
    busy = false
  }
}

// A category change is stored on the Desk too while subscribed.
function pick(c: NotifyCategory, on: boolean) {
  setCategory(c, on)
  const info = pushed.value.info
  if (!notify.push || !info) return
  refreshPush(pushRoutes, info, notify.categories).catch((e: unknown) => (failed.value = message(e)))
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
  <UModal
    v-model:open="open"
    title="Notifications"
    :description="push ? 'What raises a notification on this device, also while the Desk is closed.' : 'What raises a notification while a Desk tab is open.'"
    :ui="{ overlay: 'z-40', content: 'z-40' }"
  >
    <template #body>
      <div class="flex flex-col gap-4 text-sm" data-notify-settings :data-push-state="pushed.state">
        <p v-if="pushed.state === 'iosHint'" class="m-0 font-medium" data-notify-ios>Add the Desk to your Home Screen to get notifications</p>
        <!-- Keyed by the label: Chromium keeps a control's accessible name
             when its <label> text changes, so the switch mounts anew once
             GET /api/v1/push turns it into the device switch. -->
        <USwitch
          v-else
          :key="push ? 'device' : 'browser'"
          :model-value="mainOn"
          :label="push ? 'Notifications on this device' : 'Notifications in this browser'"
          :description="mainLine"
          :disabled="!!blocked"
          :ui="switchUi(!!blocked)"
          :aria-describedby="mainLine ? 'notify-main-line' : undefined"
          data-notify-main
          @update:model-value="setOn"
        >
          <template v-if="mainLine" #description><span id="notify-main-line">{{ mainLine }}</span></template>
        </USwitch>
        <p v-if="pushed.state === 'unavailable' && pushed.info?.reason" class="m-0 text-(--text-2)" data-notify-push-reason>{{ pushed.info.reason }}</p>
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
            @update:model-value="(v: boolean) => pick(c.category, v)"
          >
            <template #description><span :id="`notify-line-${c.category}`">{{ c.description }}</span></template>
          </USwitch>
        </fieldset>
      </div>
    </template>
  </UModal>
</template>
